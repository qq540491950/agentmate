import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";

export type ConfirmRequest = {
  title: string;
  description?: string;
  confirmText?: string;
  cancelText?: string;
  destructive?: boolean;
  action: () => void;
};

// ConfirmDialog 替代
// window.confirm：原生对话框会抢占焦点，渲染在应用主题之外，且无法设置样式；这
// 样做使焦点陷阱和 Escape 键处理与应用其余部分保持一致。
export function ConfirmDialog({ request, onClose }: {
  request: ConfirmRequest | null;
  onClose: () => void;
}) {
  const destructive = request?.destructive ?? true;
  return (
    <AlertDialog
      open={!!request}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{request?.title}</AlertDialogTitle>
          {request?.description && (
            <AlertDialogDescription>{request.description}</AlertDialogDescription>
          )}
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel asChild>
            <Button variant="outline">{request?.cancelText ?? "取消"}</Button>
          </AlertDialogCancel>
          <AlertDialogAction asChild>
            <Button
              variant={destructive ? "destructive" : "default"}
              onClick={() => {
                request?.action();
                onClose();
              }}
            >
              {request?.confirmText ?? "确认"}
            </Button>
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
