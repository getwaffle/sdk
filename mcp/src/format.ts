/**
 * Renders an integer minor-unit amount the way paybridge shows money to
 * humans everywhere else in the product (`internal/notify.FormatMoney`,
 * the dashboards' `formatMoney`): dot-grouped with an `Rp` prefix for
 * IDR ("Rp100.000"), never a bare integer or a locale-comma. This is for
 * the tool result's human-readable `text` block only — `structuredContent`
 * always carries the raw integer per the API contract, so a caller doing
 * math never has to parse this string back out.
 */
export function formatMoney(amount: number, currency: string): string {
  if (currency === "IDR" || currency === "") {
    return `Rp${groupDigits(amount)}`;
  }
  return `${groupDigits(amount)} ${currency}`;
}

/** Inserts a dot every three digits from the right (75.000.000), no Intl dependency needed for an integer minor-unit amount. */
function groupDigits(n: number): string {
  const negative = n < 0;
  const digits = Math.trunc(Math.abs(n)).toString();
  const groups: string[] = [];
  for (let end = digits.length; end > 0; end -= 3) {
    groups.unshift(digits.slice(Math.max(0, end - 3), end));
  }
  return (negative ? "-" : "") + groups.join(".");
}
