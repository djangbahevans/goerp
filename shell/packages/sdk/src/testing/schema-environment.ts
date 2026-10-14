import { ActionRegistry, actionRegistry } from "../react/action-registry.js";
import {
  ModelRegistry,
  modelRegistry,
  ResourceMetadataRegistry,
  ResourceRegistry,
  resourceMetadataRegistry,
  resourceRegistry,
  schemaRegistry,
  ViewDeclarationRegistry,
  ViewExtensionRegistry,
  ViewPathRegistry,
  viewDeclarationRegistry,
  viewExtensionRegistry,
  viewPathRegistry,
} from "../schema/index.js";
import type { MetaSchema } from "../schema/types.js";

const schemas: { schema: MetaSchema | undefined }[] = [];
let undoActive: (() => void) | undefined;

function replaceMethod<T extends object, K extends keyof T>(target: T, key: K, method: T[K]): () => void {
  const descriptor = Object.getOwnPropertyDescriptor(target, key);
  Object.defineProperty(target, key, { configurable: true, writable: true, value: method });
  return () => {
    if (descriptor) Object.defineProperty(target, key, descriptor);
    else Reflect.deleteProperty(target, key);
  };
}

function applySchema(schema: MetaSchema): () => void {
  const source = { getSchema: async () => schema };
  const resources = new ResourceRegistry(source);
  const metadata = new ResourceMetadataRegistry(source);
  const models = new ModelRegistry(source);
  const views = new ViewDeclarationRegistry(source);
  const paths = new ViewPathRegistry(source);
  const extensions = new ViewExtensionRegistry(source);
  const actions = new ActionRegistry(source);
  // Fresh registry instances isolate their cached promises without discarding live caches.
  const undo = [
    replaceMethod(schemaRegistry, "getSchema", source.getSchema),
    replaceMethod(resourceRegistry, "resolve", resources.resolve.bind(resources)),
    replaceMethod(resourceMetadataRegistry, "resolve", metadata.resolve.bind(metadata)),
    replaceMethod(modelRegistry, "resolve", models.resolve.bind(models)),
    replaceMethod(viewDeclarationRegistry, "resolve", views.resolve.bind(views)),
    replaceMethod(viewPathRegistry, "resolve", paths.resolve.bind(paths)),
    replaceMethod(viewPathRegistry, "resolveRecord", paths.resolveRecord.bind(paths)),
    replaceMethod(viewExtensionRegistry, "forTarget", extensions.forTarget.bind(extensions)),
    replaceMethod(actionRegistry, "resolve", actions.resolve.bind(actions)),
  ];
  return () => {
    for (const reset of undo.toReversed()) reset();
  };
}

export function mountTestingSchema(schema?: MetaSchema): () => void {
  const entry = { schema };
  undoActive?.();
  schemas.push(entry);
  undoActive = schema ? applySchema(schema) : undefined;
  return () => {
    const index = schemas.indexOf(entry);
    if (index === -1) return;
    const active = index === schemas.length - 1;
    schemas.splice(index, 1);
    if (!active) return;
    undoActive?.();
    const previous = schemas.at(-1);
    undoActive = previous?.schema ? applySchema(previous.schema) : undefined;
  };
}
