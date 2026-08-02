import type { PostgresQueryResult } from "@/api";
import { cn } from "@/lib/utils";

type PostgresCell =
  PostgresQueryResult["statements"][number]["rows"][number][number];

export const postgresCellText = (cell?: PostgresCell) => {
  if (!cell || cell.null) {
    return "—";
  }
  return cell.base64 === undefined
    ? (cell.text ?? "")
    : `base64:${cell.base64}`;
};

export const PostgresResultTable = ({
  emptyLabel = "Run a query to see its result.",
  result,
}: {
  emptyLabel?: string;
  result: PostgresQueryResult | null;
}) => {
  if (!result) {
    return (
      <div className="grid min-h-52 flex-1 place-items-center px-6 text-center text-[10px] text-muted-foreground">
        {emptyLabel}
      </div>
    );
  }

  return (
    <div className="min-h-0 flex-1 overflow-auto">
      {result.statements.map((statement, statementIndex) => (
        <section
          className="border-b border-border"
          key={`${statementIndex.toString()}:${statement.commandTag}`}
        >
          <div className="flex items-center gap-3 border-b border-border px-4 py-2 text-[9px] text-muted-foreground">
            <span className="text-foreground">
              {statement.commandTag || "Result"}
            </span>
            <span>{statement.rows.length.toLocaleString()} rows</span>
            {statement.truncated ? <span>bounded</span> : null}
          </div>
          {statement.columns.length > 0 ? (
            <table className="w-full border-collapse text-left text-[10px]">
              <thead>
                <tr className="border-b border-border bg-muted/20">
                  {statement.columns.map((column, columnIndex) => (
                    <th
                      className="border-r border-border px-3 py-2 font-medium last:border-r-0"
                      key={`${columnIndex.toString()}:${column.name}`}
                    >
                      {column.name}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {statement.rows.map((row, rowIndex) => (
                  <tr
                    className="border-b border-border last:border-b-0 hover:bg-muted/20"
                    key={rowIndex.toString()}
                  >
                    {row.map((cell, cellIndex) => (
                      <td
                        className={cn(
                          "max-w-80 border-r border-border px-3 py-2 align-top break-all last:border-r-0",
                          cell.null && "text-muted-foreground italic"
                        )}
                        key={cellIndex.toString()}
                      >
                        {postgresCellText(cell)}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          ) : null}
        </section>
      ))}
    </div>
  );
};
