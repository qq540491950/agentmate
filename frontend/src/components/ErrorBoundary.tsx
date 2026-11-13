import { Component, type ErrorInfo, type ReactNode } from "react";
import { Button } from "@/components/ui/button";

type Props = { children: ReactNode };
type State = { error: Error | null };

/**
 * ErrorBoundary
 * 可避免单个页面的渲染崩溃导致整个窗口空白：侧边栏保持可用，用户能够重试或切换
 * 页面。
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("页面渲染失败", error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      return (
        <div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center">
          <div className="text-sm font-medium">这个页面出现了问题</div>
          <div className="max-w-md break-words text-xs text-muted-foreground">
            {this.state.error.message}
          </div>
          <Button size="sm" variant="outline" onClick={() => this.setState({ error: null })}>
            重试
          </Button>
        </div>
      );
    }
    return this.props.children;
  }
}
