import { useState } from "react";
import { Check, ChevronDown, ChevronRight, ListTodo, Loader2, X } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import {
  Popover, PopoverClose, PopoverContent, PopoverTrigger,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";

/** TodoItem mirrors the plan_todos tool payload. */
export type TodoItem = { content: string; status: string };

/** TodoListCard renders the model's session plan (plan_todos result) as a
 * checklist so multi-step work is legible at a glance. The list collapses
 * once every step is done; a live plan stays open by default. */
export function TodoListCard({ resp }: { resp: { [k: string]: any } | null }) {
  const todos: TodoItem[] = Array.isArray(resp?.todos) ? resp.todos : [];
  const done = todos.filter((t) => t.status === "completed").length;
  const allDone = todos.length > 0 && done === todos.length;
  const active = todos.filter((t) => t.status === "in_progress");
  // null follows the plan state: in-progress plans start open, settled plans
  // start folded. A click wins for the lifetime of this card.
  const [userExpanded, setUserExpanded] = useState<boolean | null>(null);
  const expanded = userExpanded ?? !allDone;
  if (todos.length === 0) return null;
  const currentLabel = active.length > 1
    ? `${active.length} 个进行中`
    : active[0]?.content ?? "";
  return (
    <div className="rounded-xl bg-panel/70 px-4 py-3">
      <button
        type="button"
        className="flex w-full min-w-0 items-center gap-2 text-left"
        aria-expanded={expanded}
        onClick={() => setUserExpanded(!expanded)}
      >
        <ListTodo className="h-4 w-4 text-accent-text" />
        <span className="text-sm font-medium">任务计划</span>
        <Badge variant="outline" className="font-normal text-2xs">
          {done}/{todos.length} 完成
        </Badge>
        {!expanded && currentLabel && (
          <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground">
            · {currentLabel}
          </span>
        )}
        <ChevronRight className={cn(
          "ml-auto h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform",
          expanded && "rotate-90",
        )} />
      </button>
      <div className={cn(
        "grid transition-[grid-template-rows] duration-200 ease-standard",
        expanded ? "grid-rows-[1fr]" : "grid-rows-[0fr]",
      )} aria-hidden={!expanded}>
        <div className="overflow-hidden">
          <div className="pt-2">
            <TodoChecklist todos={todos} />
          </div>
        </div>
      </div>
    </div>
  );
}

/** TodoChecklist is the shared checklist body used by the inline card and the
 * header popover. */
export function TodoChecklist({ todos }: { todos: TodoItem[] }) {
  return (
    <ul className="space-y-1.5">
      {todos.map((t, i) => (
        <li key={i} className="flex items-start gap-2 text-ui leading-[1.5]">
          {t.status === "completed" ? (
            <Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-success" />
          ) : t.status === "in_progress" ? (
            <Loader2 className="mt-0.5 h-3.5 w-3.5 shrink-0 animate-spin text-accent-text" />
          ) : (
            <span className="mt-0.5 inline-block h-3.5 w-3.5 shrink-0 rounded-full bg-muted" />
          )}
          <span className={cn(
            "break-words",
            t.status === "completed" && "text-muted-foreground line-through",
            t.status === "in_progress" && "font-medium",
          )}>
            {t.content}
          </span>
        </li>
      ))}
    </ul>
  );
}

/** PlanChip is the compact header entry point for the current plan. It opens
 * a popover instead of occupying a persistent bar above the composer. */
export function PlanChip({ todos, running, waiting = false, onDismiss }: {
  todos: TodoItem[];
  running: boolean;
  waiting?: boolean;
  onDismiss: () => void;
}) {
  const done = todos.filter((t) => t.status === "completed").length;
  const allDone = todos.length > 0 && done === todos.length;
  const active = todos.filter((t) => t.status === "in_progress");
  const statusText = running ? "进行中" : waiting ? "等待确认" : allDone ? "已完成" : "未完成";
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={`任务计划 ${done}/${todos.length} 完成，${statusText}`}
          className="inline-flex h-7 max-w-[190px] items-center gap-1.5 rounded-md bg-panel px-2 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          {allDone ? (
            <Check className="h-3.5 w-3.5 shrink-0 text-success" />
          ) : running ? (
            <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-accent-text" />
          ) : (
            <ListTodo className="h-3.5 w-3.5 shrink-0 text-accent-text" />
          )}
          <span className="hidden shrink-0 sm:inline">任务计划</span>
          <span className="shrink-0 tabular-nums">{done}/{todos.length}</span>
          {!allDone && active.length > 0 && (
            <span className="hidden min-w-0 truncate text-xs text-muted-foreground md:inline">
              · {active.length > 1 ? `${active.length} 个进行中` : active[0].content}
            </span>
          )}
          <ChevronDown className="h-3 w-3 shrink-0" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 max-w-[calc(100vw-2rem)] p-0">
        <div className="flex items-center gap-2 px-3 pt-3">
          <ListTodo className="h-4 w-4 shrink-0 text-accent-text" />
          <span className="text-sm font-medium">任务计划</span>
          <Badge variant="outline" className="font-normal text-2xs">
            {done}/{todos.length} 完成
          </Badge>
          <span className="ml-auto shrink-0 text-xs text-muted-foreground">{statusText}</span>
          <PopoverClose asChild>
            <button
              type="button"
              aria-label="关闭任务计划"
              className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              onClick={onDismiss}
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </PopoverClose>
        </div>
        <div className="max-h-[min(50vh,320px)] overflow-auto px-3 pb-3 pt-2">
          <TodoChecklist todos={todos} />
        </div>
      </PopoverContent>
    </Popover>
  );
}
