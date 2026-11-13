import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { TopBar } from "@/components/layout/TopBar";
import {
  applyTheme,
  cacheTheme,
  cacheThemePrefs,
  cachedTheme,
  onSystemThemeChange,
  prefsFromSettings,
  type ThemePrefs,
} from "@/lib/theme";
import { App as AppService, Config } from "@/lib/api";

// Chat
// 是落地视图并保持立即加载；设置中心（承载管理界面的位置）按需加载，因此它绝不
// 会进入启动包。
import { ChatPage } from "@/pages/ChatPage";
const SettingsPage = lazy(() => import("@/pages/SettingsPage").then((m) => ({ default: m.SettingsPage })));
import type { SettingsLeaveGuard, SettingsTab } from "@/lib/settingsModel";

export default function App() {
  const [view, setView] = useState<"chat" | "settings">("chat");
  const [settingsTab, setSettingsTab] = useState<SettingsTab | undefined>(undefined);
  const settingsLeaveGuard = useRef<SettingsLeaveGuard | null>(null);
  const openSettings = useCallback((tab?: SettingsTab) => {
    setSettingsTab(tab);
    setView("settings");
  }, []);
  const registerSettingsLeaveGuard = useCallback((guard: SettingsLeaveGuard | null) => {
    settingsLeaveGuard.current = guard;
  }, []);
  const leaveSettings = useCallback((after: () => void) => {
    const guard = settingsLeaveGuard.current;
    if (guard) guard(after);
    else after();
  }, []);

  // 全局快捷键（记录在每个页面的帮助面板中）：⌘N 启动聊天；⌘K 聚焦输入框；⌘F
  // 切换会话内搜索；⌘, 打开设置；⌘⇧[ / ⌘⇧] 循环切换会话。ChatPage 通过 window
  // 事件消费聊天快捷键，因此它们在视图切换后仍然有效。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mod = e.metaKey || e.ctrlKey;
      if (!mod || e.repeat) return;
      // ⌘⇧[ / ⌘⇧] 在会话列表中移动（美式键盘布局将该键报告为 { / }；按 code
      // 比较，因此该快捷键可在不同布局下继续生效）。
      if (e.shiftKey && e.code === "BracketLeft") {
        e.preventDefault();
        leaveSettings(() => {
          setView("chat");
          setTimeout(() => window.dispatchEvent(new CustomEvent("wb-prev-session")), 50);
        });
        return;
      }
      if (e.shiftKey && e.code === "BracketRight") {
        e.preventDefault();
        leaveSettings(() => {
          setView("chat");
          setTimeout(() => window.dispatchEvent(new CustomEvent("wb-next-session")), 50);
        });
        return;
      }
      const key = e.key.toLowerCase();
      if (key !== "n" && key !== "k" && key !== "f" && key !== "," && key !== "b") return;
      e.preventDefault();
      if (key === "b") {
        leaveSettings(() => {
          setView("chat");
          // 像 ⌘N 一样延迟：离开设置视图时，ChatPage
          // 会在下一次渲染时挂载，并且仅在那时注册监听器。
          setTimeout(() => window.dispatchEvent(new CustomEvent("wb-toggle-sidebar")), 50);
        });
        return;
      }
      if (key === ",") {
        openSettings();
        return;
      }
      leaveSettings(() => {
        setView("chat");
        // 延迟：从其他页面切换时，ChatPage
        // 会在下一次渲染时挂载——同步派发会在其监听器存在之前触发，操作将会丢失
        // 。
        const ev = new CustomEvent(key === "k" ? "wb-focus-composer" : key === "f" ? "wb-toggle-filter" : "wb-new-chat");
        setTimeout(() => window.dispatchEvent(ev), 50);
      });
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [leaveSettings, openSettings]);
  const [helpOpen, setHelpOpen] = useState(false);
  const [prefs, setPrefs] = useState<ThemePrefs>(() => cachedTheme());
  const [, forceThemeRender] = useState(0);
  const [version, setVersion] = useState("");

  // 启动：获取应用元数据，用于侧边栏版本标签。
  useEffect(() => {
    AppService.AppInfo().then((info) => setVersion(info?.version ?? "")).catch(() => {});
  }, []);

  // 启动：立即应用缓存的主题，然后从持久化设置同步。
  useEffect(() => {
    Config.GetConfig()
      .then((cfg) => {
        const s = cfg.settings;
        const next = prefsFromSettings(s);
        cacheTheme(s);
        setPrefs(next);
        applyTheme(next);
      })
      .catch(() => {});
  }, []);

  // 当操作系统外观发生变化时重新应用（themeMode === "system"）。
  useEffect(
    () => onSystemThemeChange(prefs, () => forceThemeRender((value) => value + 1)),
    [prefs],
  );

  // 侧边栏中的主题预设/模式更改会立即持久化；设置更改在用户按下保存前保持本地。
  const changeAppearance = async (patch: Partial<ThemePrefs>) => {
    const next = { ...prefs, ...patch };
    setPrefs(next);
    applyTheme(next);
    cacheThemePrefs(next);
    try {
      const cfg = await Config.GetConfig();
      cfg.settings.themePreset = next.themePreset;
      cfg.settings.themeMode = next.themeMode;
      cacheTheme(cfg.settings);
      await Config.SaveSettings(cfg.settings);
    } catch { /* offline: cache only */ }
  };

  const onAppearanceChange = (next: ThemePrefs) => {
    setPrefs(next);
    applyTheme(next);
  };

  return (
    <TooltipProvider delayDuration={450} skipDelayDuration={300}>
      <div className="flex h-screen w-screen flex-col overflow-hidden bg-background text-foreground">
        <TopBar />
        <div className="flex min-h-0 flex-1 overflow-hidden">
          <main className="min-w-0 flex-1 overflow-hidden">
            {/* key={view} resets the boundary on navigation: one crashed view must
                not keep the other behind the error screen. */}
            <ErrorBoundary key={view}>
              <Suspense fallback={<div className="p-6 text-sm text-muted-foreground">加载中…</div>}>
                {view === "chat" ? (
                  <ChatPage
                    themePreset={prefs.themePreset}
                    themeMode={prefs.themeMode}
                    onAppearanceChange={changeAppearance}
                    onOpenHelp={() => setHelpOpen(true)}
                    onOpenSettings={openSettings}
                    version={version}
                  />
                ) : (
                  <SettingsPage
                    initialTab={settingsTab}
                    onClose={() => setView("chat")}
                    version={version}
                    onAppearanceChange={onAppearanceChange}
                    onRegisterLeaveGuard={registerSettingsLeaveGuard}
                  />
                )}
              </Suspense>
            </ErrorBoundary>
          </main>
        </div>

        <Dialog open={helpOpen} onOpenChange={setHelpOpen}>
          <DialogContent className="sm:max-w-md">
            <DialogHeader>
              <DialogTitle className="text-base">快捷键与命令</DialogTitle>
            </DialogHeader>
            <div className="grid gap-2 text-sm">
              {[
                ["Enter", "发送消息"],
                ["Shift + Enter", "换行"],
                ["⌘/Ctrl + N", "新对话（全局）"],
                ["⌘/Ctrl + K", "聚焦输入框（全局）"],
                ["⌘/Ctrl + F", "会话内搜索（全局）"],
                ["⌘/Ctrl + ,", "打开设置（全局）"],
                ["⌘/Ctrl + B", "折叠 / 展开对话列表"],
                ["⌘/Ctrl + ⇧[ / ⇧]", "上一个 / 下一个会话"],
                ["↑", "编辑最后一条消息（输入框为空时）"],
                ["Esc", "取消编辑 → 停止生成 → 关闭搜索"],
                ["/export", "导出会话 Markdown"],
                ["/exporthtml", "导出会话 HTML"],
                ["/stats", "会话统计"],
                ["/todos", "查看当前任务计划"],
                ["/new", "新建对话"],
              ].map(([k, d]) => (
                <div key={k} className="flex items-center justify-between rounded-lg bg-panel/60 px-3 py-1.5">
                  <span className="font-mono text-xs">{k}</span>
                  <span className="text-xs text-muted-foreground">{d}</span>
                </div>
              ))}
            </div>
          </DialogContent>
        </Dialog>

        <Toaster position="top-center" richColors />
      </div>
    </TooltipProvider>
  );
}
