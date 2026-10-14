import { type Validator, validators } from "@goerp/sdk/form";

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
