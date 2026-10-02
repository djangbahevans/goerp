export interface ImportMap {
  imports: Record<string, string>;
}

export function updateImportMap(current: ImportMap, next: ImportMap, reload = () => window.location.reload()): void {
  const names = Object.keys(current.imports);
  if (
    names.length !== Object.keys(next.imports).length ||
    names.some((name) => current.imports[name] !== next.imports[name])
  ) {
    reload();
  }
}

export function watchImportMap(): () => void {
  const script = document.getElementById("goerp-import-map");
  if (!script?.textContent) throw new Error("Shell import map is missing");
  const current = JSON.parse(script.textContent) as ImportMap;
  const controller = new AbortController();
  let checking = false;
  let reloading = false;
  const check = async () => {
    if (document.visibilityState === "hidden" || checking || reloading) return;
    checking = true;
    try {
      const response = await fetch("/__goerp_import_map", { cache: "no-store", signal: controller.signal });
      if (!response.ok) return;
      const next = (await response.json()) as ImportMap;
      if (
        !next.imports ||
        typeof next.imports !== "object" ||
        Array.isArray(next.imports) ||
        Object.values(next.imports).some((value) => typeof value !== "string")
      )
        return;
      updateImportMap(current, next, () => {
        reloading = true;
        window.location.reload();
      });
    } catch {
      // An unavailable deployment must not interrupt the current document.
    } finally {
      checking = false;
    }
  };
  const timer = window.setInterval(() => void check(), 60_000);
  document.addEventListener("visibilitychange", check);
  return () => {
    clearInterval(timer);
    document.removeEventListener("visibilitychange", check);
    controller.abort();
  };
}
