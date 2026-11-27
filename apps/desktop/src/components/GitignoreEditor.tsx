import { useRef, type CSSProperties } from "react";

interface GitignoreEditorProps {
  value:        string;
  onChange:     (value: string) => void;
  disabled?:    boolean;
  /** 1-based line numbers to mark as invalid (red background). */
  errorLines?:  Set<number>;
  placeholder?: string;
  rows?:        number;
}

// Identical text metrics on both layers so the caret (textarea) lines up exactly
// with the coloured glyphs (pre). Inline styles win over any Tailwind line-height
// conflict (e.g. text-xs' bundled line-height vs leading-*).
const layerStyle: CSSProperties = {
  margin:      0,
  padding:     "0.75rem",
  fontFamily:  "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
  fontSize:    "0.75rem",
  lineHeight:  "1.25rem",
  tabSize:     2,
  whiteSpace:  "pre",
  overflowWrap: "normal",
  boxSizing:   "border-box",
};

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

// Build the highlighted HTML for one .gitignore line. Comments are muted,
// a leading `!` negation is amber, and glob meta (`* ? / [..]`) is accented.
// Each line is a display:block span so an invalid line can carry a background.
function highlightLine(line: string, isError: boolean): string {
  const errCls = isError ? " bg-red-500/15" : "";
  // Zero-width char keeps an empty line one row tall, aligned with the textarea.
  const zwsp = "​";
  const block = (inner: string) =>
    `<span class="block${errCls}">${inner || zwsp}</span>`;

  const trimmed = line.trimStart();
  if (trimmed.startsWith("#")) {
    return block(`<span class="text-muted-foreground/60 italic">${escapeHtml(line)}</span>`);
  }

  const leadWs = line.slice(0, line.length - trimmed.length);
  let rest = trimmed;
  let html = escapeHtml(leadWs);

  if (rest.startsWith("!")) {
    html += `<span class="text-amber-600 dark:text-amber-400 font-semibold">!</span>`;
    rest = rest.slice(1);
  }

  // Escape first, then accent meta tokens — entities never contain these chars.
  const esc = escapeHtml(rest).replace(
    /(\[[^\]]*\]?|[*?/])/g,
    '<span class="text-blue-600 dark:text-blue-400">$1</span>',
  );
  return block(html + esc);
}

/**
 * Minimal syntax-highlighting editor for .gitignore-style patterns.
 * A transparent <textarea> sits over a highlighted <pre>; scroll is kept in
 * sync. No editor dependency — the highlighting is line-based and trivial, and
 * owning it lets us paint invalid lines (errorLines) directly.
 */
export function GitignoreEditor({
  value, onChange, disabled, errorLines, placeholder, rows = 10,
}: GitignoreEditorProps) {
  const preRef = useRef<HTMLPreElement>(null);

  const html = value
    .split("\n")
    .map((line, i) => highlightLine(line, errorLines?.has(i + 1) ?? false))
    .join("");

  return (
    <div className="relative rounded-lg border border-border bg-background overflow-hidden
                    focus-within:ring-2 focus-within:ring-ring">
      <pre
        ref={preRef}
        aria-hidden
        style={layerStyle}
        className="absolute inset-0 overflow-hidden pointer-events-none text-foreground"
        dangerouslySetInnerHTML={/* nosemgrep: typescript.react.security.audit.react-dangerouslysetinnerhtml.react-dangerouslysetinnerhtml -- html is produced by highlightLine, which runs escapeHtml() over every user-derived substring; only static class/span markup is left unescaped, so no user input can reach the DOM as markup */ { __html: html }}
      />
      <textarea
        value={value}
        disabled={disabled}
        spellCheck={false}
        wrap="off"
        rows={rows}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        onScroll={(e) => {
          if (preRef.current) {
            preRef.current.scrollTop = e.currentTarget.scrollTop;
            preRef.current.scrollLeft = e.currentTarget.scrollLeft;
          }
        }}
        style={layerStyle}
        className="relative block w-full resize-none overflow-auto border-0 bg-transparent
                   text-transparent caret-foreground outline-hidden
                   placeholder:text-muted-foreground/50 disabled:opacity-50"
      />
    </div>
  );
}
