// Placeholder module entry point — @goerp/sdk and defineModule() don't exist
// yet (implementation-backlog.md #488/#489). This exists only to give
// `goerp module build`'s Vite compile step something real to compile and
// hash; it is not a functioning module registration.
interface PlaceholderModule {
  name: string;
}

const demoModule: PlaceholderModule = {
  name: "demo",
};

export default demoModule;
