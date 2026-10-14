# Demo frontend tests

The view tests use the generated API client and schema fixture with the shell's actual renderers. The fixture includes the item model's synthesized list and form views.

From the repository root, prepare the shared frontend packages and generate the client and fixture:

```sh
npm --prefix shell ci
npm --prefix shell run build -w @goerp/sdk
npm --prefix shell run build:testing -w @goerp/shell-app
go run ./cmd/goerp codegen modules/demo --local
```

Run the module checks:

```sh
cd modules/demo/frontend
npm ci
npm run typecheck
npm test
npm run build
```

`src/test-setup.ts` registers `generated.testing.json` and the module definition once. `src/views.test.tsx` supplies a complete typed item through the generated `listItems` mock. The list renderer's saved-filter request uses an explicit `apiMocks` response. The production bundle starts from `src/index.ts`, so the test setup and shell testing entry are outside its import graph.
