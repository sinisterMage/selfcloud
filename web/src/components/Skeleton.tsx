import { cn } from "../lib/cn";

export function Skeleton({ className }: { className?: string }) {
  return <div className={cn("skeleton h-4 w-full", className)} />;
}

// SkeletonRows renders n table rows of placeholder cells. Use inside a
// <tbody> while a useQuery is loading.
export function SkeletonRows({ rows, cols }: { rows: number; cols: number }) {
  return (
    <>
      {Array.from({ length: rows }).map((_, r) => (
        <tr key={r} className="border-t border-border">
          {Array.from({ length: cols }).map((_, c) => (
            <td key={c} className="px-4 py-3">
              <Skeleton className="h-4 w-full max-w-[180px]" />
            </td>
          ))}
        </tr>
      ))}
    </>
  );
}
