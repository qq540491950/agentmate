import { Fragment, type ReactNode } from "react";
import { cn } from "@/lib/utils";
import { UserQuote } from "./MessageBubble";
import type { ChatItem, TurnItem } from "@/lib/chatUtils";

export type TurnBodyOpts = {
  /** isLastAssistant marks the conversation's final reply (regenerate lives
   *  there), mirroring the pre-turn-card flat list semantics. */
  isLastAssistant: boolean;
  /** liveGroup marks execution blocks of the still-running turn: their
   *  whole-run clock keeps ticking until the turn settles. */
  liveGroup: boolean;
};

/** TurnCard renders one conversation turn as a single card: the user input
 *  as a compact quote header on top, the run's output below. The turn the
 *  reader is currently at (rail selection or scroll position) gets the brand
 *  highlight. */
export function TurnCard({ turn, active, live, isLastTurn, onSelectTurn, onEdit, canEdit, filterQ, renderBodyItem }: {
  turn: TurnItem;
  active: boolean;
  live: boolean;
  isLastTurn: boolean;
  onSelectTurn: (msgId: string) => void;
  onEdit: (text: string) => void;
  canEdit: boolean;
  filterQ: string;
  renderBodyItem: (item: ChatItem, opts: TurnBodyOpts) => ReactNode;
}) {
  const lastBody = turn.body[turn.body.length - 1];
  return (
    <div
      className={cn(
        "wb-message-in rounded-2xl px-3 py-2.5 ring-1 transition-colors",
        active ? "bg-brand/5 ring-brand/30" : "bg-muted/30 ring-border",
      )}
    >
      <UserQuote
        m={turn.header}
        active={active}
        onSelectTurn={onSelectTurn}
        onEdit={onEdit}
        canEdit={canEdit}
        filterQ={filterQ}
      />
      {turn.body.length > 0 && (
        <div className="mt-1.5 space-y-2">
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
