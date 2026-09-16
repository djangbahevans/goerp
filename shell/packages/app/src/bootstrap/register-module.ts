import type { ModuleDefinition } from "@goerp/sdk";
import { commandRegistry } from "../chrome/command-registry.js";
import type { Command } from "../chrome/command-types.js";

// The one piece of a loaded module's ModuleDefinition that defineModule()
// itself can't register directly: CommandRegistry (the real, rendered
// Cmd+K palette) is shell-app-owned, and @goerp/sdk can't import it
// without inverting the SDK/app dependency direction — everything else
// (views, fieldRenderers, batchLoaders, navigation, errorHandlers) is
// already registered by the time defineModule() returns, as a side effect
// of the bundle's own top-level code running during import.
export function registerModule(definition: ModuleDefinition): () => void {
  if (!definition.commands || definition.commands.length === 0) {
    return () => {};
  }
  const commands: Command[] = definition.commands;
  return commandRegistry.register(commands);
}
