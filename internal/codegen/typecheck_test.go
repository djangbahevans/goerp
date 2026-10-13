package codegen

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
)

const typecheckUsage = `import { actionRef, resourceRef, permissionRef, resourceApi, callAction, defineModule, type ModuleApis, type CreateOf, type UpdateOf } from '@goerp/sdk';
import { useModule, useRecord, useInfiniteList, useAction, useRecordActivity, useRecordFollowers, useRecordReaders, useScheduledActivities } from '@goerp/sdk/react';
import { usePermission, useOptionalPermission, useFieldPermission, type CanProps } from '@goerp/sdk/auth';
import { AppError } from '@goerp/sdk/error';
import { isRecordChange, type RealtimeMessage, type RecordChange } from '@goerp/sdk/realtime';
import {
  contactsApi, type Contact, type ContactCreate,
  useArchiveContact, useContact, useContacts, useDeleteCache, useGetByEmail, useMergeContact, usePostImport, useReindexContact,
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
  void contactsApi.reindexContact();
  void contactsApi.mergeContact({ target_id: 'a', source_ids: ['b'], note: null });
  // @ts-expect-error — NoBody record actions accept only an id
  void contactsApi.archiveContact('c1', {});
  // @ts-expect-error — NoBody collection actions accept no arguments
  void contactsApi.reindexContact({});
  // @ts-expect-error — the action request requires source_ids
  void contactsApi.mergeContact({ target_id: 'a' });
  // @ts-expect-error — ignored Go fields are absent from the request interface
  void contactsApi.mergeContact({ target_id: 'a', source_ids: [], Internal: 'private' });
  void contactsApi.getByEmail('a@b.c');
  void contactsApi.getSearch().then((page) => page.meta.hasMore);
  const name: string | undefined = useContact('c1').record?.name;
  const pages = useContacts({ limit: 5 }).data;
  const byEmail: Contact | undefined = useGetByEmail('a@b.c').data;
  useMergeContact().mutate({ target_id: 'a', source_ids: [] });
  useArchiveContact().mutate({ id: 'c1' });
  useReindexContact().mutate();
  // @ts-expect-error — NoBody record mutations have no body member
  useArchiveContact().mutate({ id: 'c1', body: {} });
  // @ts-expect-error — NoBody collection mutations take no payload
  useReindexContact().mutate({});
  usePostImport().mutate({ body: { rows: [{ name: 'x', email: null }] } });
  useDeleteCache().mutate();
  const record = useRecord('contacts.contact', 'c1');
  const typed: Contact | undefined = record.record;
  record.setField('name', 'Ada');
  record.setFields({ email: null });
  // @ts-expect-error undeclared resource
  useRecord('contacts.contcat', 'c1');
  // @ts-expect-error another module needs a ref
  resourceApi.get('hr.employee', 'e1');
  // @ts-expect-error undeclared action
  useAction('contacts.mergo');
  // @ts-expect-error undeclared permission
  usePermission('contacts:contact:reed');
  // @ts-expect-error optional permission is checked too
  useOptionalPermission('hr:employee:read');
  // @ts-expect-error undeclared field
  useFieldPermission('contacts.contact', 'naem');
  useFieldPermission('contacts.contact', 'name');
  // @ts-expect-error wrong field value
  record.setField('name', 42);
  // @ts-expect-error read-only field
  record.setField('id', 'new-id');
  // @ts-expect-error setFields excludes read-only fields
  record.setFields({ created_at: 'today' });
  // @ts-expect-error setFields checks values
  record.setFields({ is_active: 'yes' });
  // @ts-expect-error undeclared filter field
  useInfiniteList('contacts.contact', { filter: { naem: 'Ada' } });
  // @ts-expect-error undeclared sort field
  resourceApi.list('contacts.contact', { sort: '-naem' });
  useInfiniteList('contacts.contact', { sort: ['-created_at', 'name'] });
  // @ts-expect-error create requires type
  resourceApi.create('contacts.contact', { name: 'Ada' });
  // @ts-expect-error create is disabled on company
  resourceApi.create('contacts.company', {});
  // @ts-expect-error update is disabled on company
  resourceApi.update('contacts.company', 'c1', {});
  // @ts-expect-error action variables are required
  callAction('contacts.merge');
  // @ts-expect-error wrong action variables
  callAction('contacts.archive', { body: {} });
  // @ts-expect-error NoBody accepts no variables
  callAction('contacts.reindex', {});
  callAction('contacts.reindex');
  useAction('contacts.convert').mutate({ id: 'c1' });
  // @ts-expect-error workflow transitions accept an id object
  useAction('contacts.convert').mutate('c1');
  const permission: CanProps['do'] = 'contacts:contact:read';
  // @ts-expect-error Can checks permission names
  const badPermission: CanProps['do'] = 'hr:employee:read';
  void badPermission;
  useModule('contacts').can(permission);
  // @ts-expect-error ModuleContext.can checks permission names
  useModule('contacts').can('contacts:contact:reed');
  defineModule({ name: 'contacts', commands: [{ id: 'read', label: 'Read', permission, action() {} }] });
  // @ts-expect-error command permissions are checked
  defineModule({ name: 'contacts', commands: [{ id: 'read', label: 'Read', permission: 'contacts:contact:reed', action() {} }] });
  defineModule({ name: 'contacts', errorHandlers: { 'contacts.contact.duplicate_email': () => {}, '*': () => {} } });
  // @ts-expect-error undeclared handler code
  defineModule({ name: 'contacts', errorHandlers: { 'contacts.contact.duplciate_email': () => {} } });
  const err = new AppError({ code: 'runtime', message: 'Failure', httpStatus: 400 });
  err.is('contacts.contact.duplicate_email');
  // @ts-expect-error undeclared error code
  err.is('contacts.contact.duplciate_email');
  // @ts-expect-error another module needs a resource ref
  useRecordActivity('hr.employee', 'e1');
  // @ts-expect-error followers check model names
  useRecordFollowers('contacts.contcat', 'c1');
  useRecordReaders('contacts.contact', 'c1', null);
  // @ts-expect-error readers check model names
  useRecordReaders('contacts.contcat', 'c1', null);
  // @ts-expect-error scheduled activities check model names
  useScheduledActivities('contacts.contcat', 'c1');
  const employee = resourceRef<{ id: string; name: string }>('hr.employee');
  const employeeRecord = useRecord(employee, 'e1');
  employeeRecord.setField('name', 'Ada');
  // @ts-expect-error ref fields retain their types
  employeeRecord.setField('name', 42);
  usePermission(permissionRef('hr:employee:read'));
  const external: Promise<{ ok: boolean }> = callAction(actionRef<{ ok: boolean }, { id: string }>('hr.approve'), { id: 'e1' });
  useAction(actionRef<{ ok: boolean }, void>('hr.refresh')).mutate();
  const message: RealtimeMessage = { type: 'record.updated', event: 'record.updated' };
  if (isRecordChange(message, 'contacts.contact')) {
    const change: RecordChange<'contacts.contact'> = message;
    const snapshot: Contact | undefined = change.record;
    void snapshot;
  }
  // @ts-expect-error another module's client has unknown type
  useModule('billing').api.listContacts();
  const moduleApi: ModuleApis['contacts'] = api;
  const create: CreateOf<'contacts.contact'> = body;
  const update: UpdateOf<'contacts.contact'> = { name: 'Ada' };
  return [list, name, pages, byEmail, typed, external, moduleApi, create, update];
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

func TestGenerate_ExampleClientTypeChecks(t *testing.T) {
	typecheck(t, map[string][]byte{
		"generated.ts": generateExample(t, copyExampleModule(t)),
		"usage.ts":     []byte(typecheckUsage),
	})
}

func TestSDKWithoutGeneratedRegistries(t *testing.T) {
	typecheck(t, map[string][]byte{
		"usage.ts": []byte(`
import { callAction, resourceApi, type ResourceName, type ActionName, type PermissionName, type ErrorCodeName, type RecordOf, type VariablesOf, type ResultOf } from '@goerp/sdk';
import { useRecord, useAction } from '@goerp/sdk/react';
type Equal<A, B> = (<T>() => T extends A ? 1 : 2) extends (<T>() => T extends B ? 1 : 2) ? true : false;
const resource: Equal<ResourceName, string> = true;
const action: Equal<ActionName, string> = true;
const permission: Equal<PermissionName, string> = true;
const error: Equal<ErrorCodeName, string> = true;
const record: Equal<RecordOf<'runtime.model'>, Record<string, unknown>> = true;
const variables: Equal<VariablesOf<'runtime.action'>, unknown> = true;
const result: Equal<ResultOf<'runtime.action'>, unknown> = true;
export const checks = [resource, action, permission, error, record, variables, result];
export function useRuntime(resource: string, action: string) {
  useRecord(resource, 'id').setFields({ arbitrary: true });
  useAction(action).mutate({ any: 'payload' });
  resourceApi.list(resource, { sort: ['-runtime_field', 'other'], filter: { runtime_field: 'value' } });
  return callAction(action, undefined);
}
`),
	})
}

func TestSDKEmptyModuleRegistries(t *testing.T) {
	out, err := Generate(&Input{Module: "empty"})
	if err != nil {
		t.Fatal(err)
	}

	typecheck(t, map[string][]byte{
		"generated.ts": out,
		"usage.ts": []byte(`
import { resourceRef, actionRef, permissionRef, resourceApi, callAction, type ErrorCodeName } from '@goerp/sdk';
import { usePermission } from '@goerp/sdk/auth';
import { AppError } from '@goerp/sdk/error';
export const code: [ErrorCodeName] extends [never] ? true : false = true;
// @ts-expect-error empty resource registry accepts no literal
resourceApi.get('empty.model', 'id');
// @ts-expect-error empty action registry accepts no literal
callAction('empty.action', undefined);
// @ts-expect-error empty permission registry accepts no literal
usePermission('empty:read');
// @ts-expect-error empty error registry accepts no literal
new AppError({code: 'runtime', message: 'failed', httpStatus: 400}).is('empty.failed');
resourceApi.get(resourceRef('foreign.model'), 'id');
callAction(actionRef<unknown, void>('foreign.action'));
usePermission(permissionRef('foreign:read'));
`),
	})
}

func TestSDKListActionRegistry(t *testing.T) {
	in := testInput([]Model{contactModel("list", "get")}, Route{
		Model:          "contacts.contact",
		Name:           "search",
		Scope:          CollectionScope,
		ResponseIsList: true,
		ResponseType:   &abiv1.TypeDesc{Kind: abiv1.TypeKindObject, Name: "Contact"},
	})
	out, err := Generate(in)
	if err != nil {
		t.Fatal(err)
	}

	typecheck(t, map[string][]byte{
		"generated.ts": out,
		"usage.ts": []byte(`
import { callAction, type PagedResponse, type PagedResponseWire } from '@goerp/sdk';
import { useAction } from '@goerp/sdk/react';
import { contactsApi, useSearchContact, type Contact } from './generated.js';
export function useSearch() {
  const wire: Promise<PagedResponseWire<Contact>> = callAction('contacts.search');
  const page: Promise<PagedResponse<Contact>> = contactsApi.searchContact();
  const hasMore: boolean | undefined = useAction('contacts.search').data?.meta.has_more;
  const generatedHasMore: boolean | undefined = useSearchContact().data?.meta.has_more;
  return [wire, page, hasMore, generatedHasMore];
}
`),
	})
}

func typecheck(t *testing.T, files map[string][]byte) {
	t.Helper()

	shellDir, err := filepath.Abs(filepath.Join("..", "..", "shell"))
	if err != nil {
		t.Fatal(err)
	}
	tsc := filepath.Join(shellDir, "node_modules", ".bin", "tsc")
	if _, err := os.Stat(tsc); err != nil {
		t.Skipf("shell dependencies not installed (run npm ci in shell/): %v", err)
	}

	// Keep the fixture beneath shell so Node resolves the workspace dependencies.
	dir, err := os.MkdirTemp(shellDir, ".codegen-typecheck-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	files["tsconfig.json"] = []byte(typecheckConfig)
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, tsc, "-p", dir)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tsc: %v\n%s", err, combined)
	}
}
