import { Fragment, type ReactNode } from "react";
import { UserQuote } from "./MessageBubble";
import { TurnStatus } from "./TurnStatus";
import type { ChatItem, TurnItem } from "@/lib/chatUtils";

export type TurnBodyOpts = {
  /** isLastAssistant marks the conversation's final reply (regenerate lives
   *  there), mirroring the pre-turn-card flat list semantics. */
  isLastAssistant: boolean;
  /** liveGroup marks execution blocks of the still-running turn: their
   *  whole-run clock keeps ticking until the turn settles. */
  liveGroup: boolean;
};

/** TurnBlock renders one conversation turn as two independent blocks: the
 *  user input as a right-aligned bubble, and the run's output as a separate
 *  left-aligned body. Inputs and replies never share an outer card; the turn
 *  the reader is currently at highlights only the user bubble. */
export function TurnBlock({ turn, active, live, isLastTurn, waiting = false, onEdit, canEdit, filterQ, renderBodyItem }: {
  turn: TurnItem;
  active: boolean;
  live: boolean;
  isLastTurn: boolean;
  /** waiting shows the pre-first-token status row under the user bubble. */
  waiting?: boolean;
  onEdit: (text: string) => void;
  canEdit: boolean;
  filterQ: string;
  renderBodyItem: (item: ChatItem, opts: TurnBodyOpts) => ReactNode;
}) {
  const lastBody = turn.body[turn.body.length - 1];
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
          {turn.body.map((item) => (
            <Fragment key={item.id}>
              {renderBodyItem(item, {
                isLastAssistant: isLastTurn
                  && item === lastBody
                  && item.kind === "message"
                  && item.message.kind === "assistant",
                liveGroup: live && item.kind === "tool_group",
              })}
            </Fragment>
          ))}
        </div>
      )}
    </div>
  );
}
