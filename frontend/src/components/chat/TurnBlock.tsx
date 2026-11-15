import { Fragment, type ReactNode } from "react";
import { UserQuote } from "./MessageBubble";
import { TurnStatus } from "./TurnStatus";
import type { ChatItem, TurnItem } from "@/lib/chatUtils";

export type TurnBodyOpts = {
  /**
   * isLastAssistant
   * 标记会话的最终回复（重新生成位于此处），与回合卡片之前的扁平列表语义保持一
   * 致。
   */
  isLastAssistant: boolean;
  /** liveGroup 标记仍在运行的回合的最后一个执行块：只有它可以持续计时并保持
   *  运行态，之前的执行块已经结算。 */
  liveGroup: boolean;
};

/**
 * TurnBlock
 * 将一个会话回合渲染为两个独立块：用户输入为右对齐气泡，本次运行的输出为单独的
 * 左对齐主体。输入与回复绝不共用外层卡片；读者当前所在的回合仅高亮用户气泡。
 */
export function TurnBlock({ turn, active, live, isLastTurn, waiting = false, onEdit, canEdit, filterQ, renderBodyItem }: {
  turn: TurnItem;
  active: boolean;
  live: boolean;
  isLastTurn: boolean;
  /** waiting 会在用户气泡下方显示首个 token 之前的状态行。 */
  waiting?: boolean;
  onEdit: (text: string) => void;
  canEdit: boolean;
  filterQ: string;
  renderBodyItem: (item: ChatItem, opts: TurnBodyOpts) => ReactNode;
}) {
  const lastBody = turn.body[turn.body.length - 1];
  let lastGroupIndex = -1;
  for (let i = turn.body.length - 1; i >= 0; i--) {
    if (turn.body[i].kind === "tool_group") {
      lastGroupIndex = i;
      break;
    }
  }
  return (
    <div data-slot="turn-block" className="space-y-3">
      <UserQuote
        m={turn.header}
        active={active}
        onEdit={onEdit}
        canEdit={canEdit}
        filterQ={filterQ}
      />
      {waiting && turn.body.length === 0 && <TurnStatus />}
      {turn.body.length > 0 && (
        <div data-slot="turn-body" className="space-y-2">
          {turn.body.map((item, index) => (
            <Fragment key={item.id}>
              {renderBodyItem(item, {
                isLastAssistant: isLastTurn
                  && item === lastBody
                  && item.kind === "message"
                  && item.message.kind === "assistant",
                liveGroup: live && item.kind === "tool_group" && index === lastGroupIndex,
              })}
            </Fragment>
          ))}
        </div>
      )}
    </div>
  );
}
