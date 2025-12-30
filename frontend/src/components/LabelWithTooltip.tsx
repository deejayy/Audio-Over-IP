import * as Tooltip from "@radix-ui/react-tooltip";
import { Info } from "lucide-react";
import { cn } from "@/lib/utils";

type ValidTooltipPositions = "right" | "left";

interface LabelWithTooltipProps {
    label: string;
    tooltip: string;
    position?: ValidTooltipPositions;
}

export function LabelWithTooltip({ label, tooltip, position = "right" }: LabelWithTooltipProps) {
    return (
        <div className={cn("flex items-center gap-2 group", position === "right" ? "justify-end" : "justify-start")}>
            <div className="text-sm font-semibold tracking-tight text-foreground group-hover:text-primary transition-colors cursor-default w-fit">
                {label}
            </div>
            <Tooltip.Root>
                <Tooltip.Trigger asChild>
                    <button type="button" className="outline-none">
                        <Info className="h-4 w-4 text-muted-foreground hover:text-primary transition-colors cursor-help" />
                    </button>
                </Tooltip.Trigger>
                <Tooltip.Portal>
                    <Tooltip.Content
                        className="z-[120] max-w-[240px] rounded-lg bg-popover border border-border px-3 py-2 text-xs text-popover-foreground shadow-2xl animate-in fade-in-0 zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=top]:slide-in-from-bottom-2"
                        sideOffset={8}
                    >
                        {tooltip}
                        <Tooltip.Arrow className="fill-border" />
                    </Tooltip.Content>
                </Tooltip.Portal>
            </Tooltip.Root>
        </div>
    );
}
