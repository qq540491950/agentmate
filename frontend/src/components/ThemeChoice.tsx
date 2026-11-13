import { SegmentedControl } from "@/components/ui/choice";
import { SettingRow } from "@/components/layout/PageShell";
import { THEME_OPTIONS, normalizeThemePreset, type ThemePreset } from "@/lib/theme";

/** ThemeChoice 让四个预设与其他外观设置保持同样紧凑的控件节奏；实时 UI 就是主题预览。 */
export function ThemeChoice({
  value,
  onChange,
}: {
  value: string;
  onChange: (value: ThemePreset) => void;
}) {
  const current = normalizeThemePreset(value);
  const description = THEME_OPTIONS.find((option) => option.value === current)?.description;
  return (
    <SettingRow label="主题" description={description}>
      <SegmentedControl
        ariaLabel="主题"
        value={current}
        options={THEME_OPTIONS}
        onChange={onChange}
      />
    </SettingRow>
  );
}
