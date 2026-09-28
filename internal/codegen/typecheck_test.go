package codegen

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// typecheckUsage exercises the example module's generated client the way
// module code would, including through useModule's api binding.
const typecheckUsage = `import { useModule } from '@goerp/sdk/react';
import {
  contactsApi, type Contact, type ContactCreate,
  useArchiveContact, useContact, useContacts, useDeleteCache, useGetByEmail, useMergeContact, usePostImport,
} from './generated.js';

export function useUsage() {
  const api = useModule('contacts').api;
  const list: Promise<{ data: Contact[] }> = api.listContacts({ filter: { is_active: true }, limit: 10 });
  const body: ContactCreate = { type: 'person', name: 'Ada' };
  void api.createContact(body);
  void api.updateContact('c1', { email: null }, 'etag');
  // @ts-expect-error — a create without a required field
  void api.createContact({ name: 'Ada' });
  // @ts-expect-error — the api is typed, so an unknown function fails
  void api.noSuchFunction();
  void contactsApi.mergeContact({ target_id: 'a', source_ids: ['b'] });
  void contactsApi.archiveContact('c1');
  void contactsApi.getByEmail('a@b.c');
  void contactsApi.getSearch().then((page) => page.meta.hasMore);
  const name: string | undefined = useContact('c1').record?.name;
  const pages = useContacts({ limit: 5 }).data;
  const byEmail: Contact | undefined = useGetByEmail('a@b.c').data;
  useMergeContact().mutate({ target_id: 'a', source_ids: [] });
  useArchiveContact().mutate({ id: 'c1' });
  usePostImport().mutate({ body: { rows: [{ name: 'x', email: null }] } });
  useDeleteCache().mutate();
  return [list, name, pages, byEmail];
}
`

// typecheckConfig compiles the generated file with the shell's own strict
// compiler options, resolving @goerp/sdk to the SDK's sources.
const typecheckConfig = `{
  "extends": "../tsconfig.json",
  "compilerOptions": {
    "noEmit": true,
    "composite": false,
    "declaration": false,
    "declarationMap": false,
    "paths": {
      "@goerp/sdk": ["./../packages/sdk/src/index.ts"],
      "@goerp/sdk/*": ["./../packages/sdk/src/*/index.ts"]
    }
  },
  "include": ["*.ts"]
}
`

// TestGenerate_ExampleClientTypeChecks runs the shell's tsc over the
// example module's generated client and a file that uses it. It skips when
// the shell's node_modules aren't installed (npm ci in shell/).
func TestGenerate_ExampleClientTypeChecks(t *testing.T) {
	shellDir, err := filepath.Abs(filepath.Join("..", "..", "shell"))
	if err != nil {
		t.Fatal(err)
	}
	tsc := filepath.Join(shellDir, "node_modules", ".bin", "tsc")
	if _, err := os.Stat(tsc); err != nil {
		t.Skipf("shell dependencies not installed (run npm ci in shell/): %v", err)
	}

	out := generateExample(t, copyExampleModule(t))

	// Inside shell/, so @tanstack/react-query and react resolve from the
	// shell's own node_modules.
	dir, err := os.MkdirTemp(shellDir, ".codegen-typecheck-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	for name, content := range map[string][]byte{
		"generated.ts":  out,
		"usage.ts":      []byte(typecheckUsage),
		"tsconfig.json": []byte(typecheckConfig),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, tsc, "-p", dir)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tsc: %v\n%s\ngenerated.ts:\n%s", err, combined, out)
	}
}
