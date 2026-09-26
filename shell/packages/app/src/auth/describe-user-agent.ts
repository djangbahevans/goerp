// auth-internals.md §4 "Session management endpoints": the raw user_agent
// reaches the client unparsed and the shell labels it "browser on OS".
const BROWSERS: [RegExp, string][] = [
  [/Edg\//, "Edge"],
  [/OPR\/|Opera/, "Opera"],
  [/Firefox\//, "Firefox"],
  [/Chrome\/|CriOS\//, "Chrome"],
  [/Safari\//, "Safari"],
];

const SYSTEMS: [RegExp, string][] = [
  [/iPhone|iPad|iPod/, "iOS"],
  [/Android/, "Android"],
  [/Windows/, "Windows"],
  [/Mac OS X|Macintosh/, "macOS"],
  [/CrOS/, "ChromeOS"],
  [/Linux/, "Linux"],
];

function firstMatch(userAgent: string, table: [RegExp, string][]): string | undefined {
  return table.find(([pattern]) => pattern.test(userAgent))?.[1];
}

export function describeUserAgent(userAgent: string | null): string {
  if (!userAgent) return "Unknown device";
  const browser = firstMatch(userAgent, BROWSERS);
  const system = firstMatch(userAgent, SYSTEMS);
  if (browser && system) return `${browser} on ${system}`;
  return browser ?? system ?? "Unknown device";
}
