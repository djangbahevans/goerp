import type { TextInputProps } from "@goerp/sdk/components";
import { type FieldProps, type FormController, useForm, type Validator, validators } from "@goerp/sdk/form";

const email: Validator = validators.compose(validators.required, validators.email);
email(undefined);
email("user@example.com");

const custom: Validator<string> = (value) => (value.startsWith("A") ? undefined : "Must start with A");
const name = validators.compose(validators.required, custom, validators.minLength(3));
name("Ama");
// @ts-expect-error Composed rules retain the custom validator's input type.
name(42);

validators.min(0)(1);
validators.maxLength(3)("abc");
// @ts-expect-error Factory limits must be numbers.
validators.minLength("3");
// @ts-expect-error Rules must return a message or undefined.
validators.compose((_value: string) => false);

export function useFormTypeChecks() {
  const form = useForm({ defaultValues: { name: "", count: 0, agree: false } });
  const controller: FormController<{ name: string; count: number; agree: boolean }> = form;
  const text: TextInputProps = controller.register("name");
  void text;
  const field: FieldProps<string> = controller.register("name");
  field.onChange("Ama");
  form.setValue("name", "Ama");
  form.setValue("count", 42);
  form.setValues({ agree: true });
  form.setErrors({ name: ["Required"] });
  form.handleSubmit((values) => {
    values.name.toUpperCase();
  });
  // @ts-expect-error A string field cannot bind a numeric value control.
  const wrongText: TextInputProps = form.register("count");
  void wrongText;
  // @ts-expect-error A numeric value cannot be assigned to a string field.
  form.setValue("name", 42);
  // @ts-expect-error A partial update preserves each field's value type.
  form.setValues({ count: "42" });
  // @ts-expect-error Registration requires a known form field.
  form.register("missing");
  // @ts-expect-error Server errors contain arrays of messages.
  form.setErrors({ name: "Required" });
}
