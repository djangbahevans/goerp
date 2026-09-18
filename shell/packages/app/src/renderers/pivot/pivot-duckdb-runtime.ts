import * as duckdb from "@duckdb/duckdb-wasm";
import duckdbWorkerEH from "@duckdb/duckdb-wasm/dist/duckdb-browser-eh.worker.js?url";
import duckdbWorkerMVP from "@duckdb/duckdb-wasm/dist/duckdb-browser-mvp.worker.js?url";
import duckdbWasmEH from "@duckdb/duckdb-wasm/dist/duckdb-eh.wasm?url";
import duckdbWasmMVP from "@duckdb/duckdb-wasm/dist/duckdb-mvp.wasm?url";

const BUNDLES: duckdb.DuckDBBundles = {
  mvp: { mainModule: duckdbWasmMVP, mainWorker: duckdbWorkerMVP },
  eh: { mainModule: duckdbWasmEH, mainWorker: duckdbWorkerEH },
};

let dbPromise: Promise<duckdb.AsyncDuckDB> | null = null;

// One DuckDB-WASM instance, in its own Web Worker, shared by every pivot
// view in the shell for the process lifetime — instantiating it per view
// would mean re-downloading and re-compiling the WASM module on every
// mount.
export function getDuckDB(): Promise<duckdb.AsyncDuckDB> {
  dbPromise ??= (async () => {
    const bundle = await duckdb.selectBundle(BUNDLES);
    const worker = new Worker(bundle.mainWorker ?? BUNDLES.mvp.mainWorker);
    const logger = new duckdb.ConsoleLogger(duckdb.LogLevel.WARNING);
    const db = new duckdb.AsyncDuckDB(logger, worker);
    await db.instantiate(bundle.mainModule, bundle.pthreadWorker);
    return db;
  })().catch((err: unknown) => {
    // A transient failure (e.g. the WASM binary fetch dropping) must not
    // wedge every future call behind one cached rejected promise — the
    // next getDuckDB() call should retry from scratch.
    dbPromise = null;
    throw err;
  });
  return dbPromise;
}
