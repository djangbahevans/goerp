import { summarizeIssues, viewExtensionRegistry } from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import * as v from "valibot";
import { type FormField, FormFieldSchema, type FormSection, type FormViewDeclaration } from "./form-view-types.js";

interface FieldExtension {
  target: string;
  position: "append" | "prepend";
  fields: FormField[];
}

function extendSections(sections: FormSection[], extensions: FieldExtension[]): FormSection[] {
  return sections.map((section) => {
    if (!section.name || (section.type !== undefined && section.type !== "fields" && section.type !== "header")) {
      return section;
    }
    const matching = extensions.filter((extension) => extension.target === section.name);
    return {
      ...section,
      fields: [
        ...matching.filter((extension) => extension.position === "prepend").flatMap((extension) => extension.fields),
        ...(section.fields ?? []),
        ...matching.filter((extension) => extension.position === "append").flatMap((extension) => extension.fields),
      ],
    };
  });
}

export function useFormFieldExtensions(view: FormViewDeclaration, module: string): FormViewDeclaration {
  const { data: extensions = [] } = useQuery({
    queryKey: ["form-field-extensions", module, view.name],
    queryFn: async (): Promise<FieldExtension[]> => {
      const entries = await viewExtensionRegistry.forTarget(module, view.name);
      const resolved: FieldExtension[] = [];
      for (const entry of entries) {
        if (entry.definition?.type !== "fields" || !entry.definition.target_section || !entry.definition.position)
          continue;
        const result = v.safeParse(v.array(FormFieldSchema), entry.definition.fields);
        if (!result.success) {
          console.warn(
            `Field extension "${entry.ref.extension}" has invalid fields: ${summarizeIssues(result.issues)}`,
          );
          continue;
        }
        resolved.push({
          target: entry.definition.target_section,
          position: entry.definition.position,
          fields: result.output,
        });
      }
      return resolved;
    },
  });

  return {
    ...view,
    sections: extendSections(view.sections, extensions),
    ...(view.tabs
      ? {
          tabs: view.tabs.map((tab) => ({
            ...tab,
            ...(tab.sections ? { sections: extendSections(tab.sections, extensions) } : {}),
          })),
        }
      : {}),
  };
}
