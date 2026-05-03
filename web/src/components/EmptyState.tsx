import type { LucideIcon } from "lucide-react";

interface Props {
  icon?: LucideIcon;
  title: string;
  description?: string;
  action?: React.ReactNode;
}

export default function EmptyState({ icon: Icon, title, description, action }: Props) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-14 px-6 text-center">
      {Icon && (
        <div className="rounded-full border border-border bg-elevated p-3 text-muted">
          <Icon size={20} />
        </div>
      )}
      <div>
        <div className="font-medium text-fg">{title}</div>
        {description && <div className="mt-1 text-sm text-muted max-w-md">{description}</div>}
      </div>
      {action}
    </div>
  );
}
