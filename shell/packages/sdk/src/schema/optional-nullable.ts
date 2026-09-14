import * as v from "valibot";

// A manifest field may be JSON `null` as well as omitted (manifest-spec.md's
// own examples do this) — both normalize to `undefined` here.
export function optionalNullable<T>(schema: v.GenericSchema<unknown, T>) {
  return v.optional(
    v.pipe(
      v.nullable(schema),
      v.transform((value) => value ?? undefined),
    ),
  );
}
