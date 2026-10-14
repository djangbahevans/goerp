import { type SubmitEvent, useRef, useState } from "react";

export interface FieldProps<V = unknown> {
  name: string;
  value: V;
  onChange: (value: V) => void;
  onBlur: () => void;
}

export interface FormController<T extends object> {
  values: T;
  errors: Partial<Record<keyof T, string>>;
  touched: Partial<Record<keyof T, boolean>>;
  isDirty: boolean;
  isValid: boolean;
  register: <K extends keyof T>(field: K) => FieldProps<T[K]>;
  setValue: <K extends keyof T>(field: K, value: T[K]) => void;
  setValues: (values: Partial<T>) => void;
  setErrors: (errors: Record<string, string[]>) => void;
  clearError: (field: keyof T) => void;
  reset: (values?: T) => void;
  handleSubmit: (fn: (values: T) => void) => (event: SubmitEvent<HTMLFormElement>) => void;
}

export interface FormOptions<T extends object> {
  defaultValues: T;
  validate?: (values: T) => Partial<Record<keyof T, string>>;
}

interface FormState<T extends object> {
  values: T;
  errors: Partial<Record<keyof T, string>>;
  touched: Partial<Record<keyof T, boolean>>;
  baseline: T;
}

export function useForm<T extends object>({ defaultValues, validate }: FormOptions<T>): FormController<T> {
  const [state, setState] = useState<FormState<T>>(() => ({
    values: { ...defaultValues },
    baseline: { ...defaultValues },
    errors: {},
    touched: {},
  }));
  const defaults = useRef(state.values);
  const current = useRef(state);
  const fields = Object.keys(defaults.current) as (keyof T)[];

  // Event handlers share pending state so batched setters and submission see every edit.
  function update(next: FormState<T>): void {
    current.current = next;
    setState(next);
  }

  function setValues(patch: Partial<T>): void {
    const previous = current.current;
    const changed = fields.filter((field) => Object.hasOwn(patch, field));
    const values = { ...previous.values };
    const errors = { ...previous.errors };

    for (const field of changed) {
      values[field] = patch[field] as T[typeof field];
      delete errors[field];
    }

    if (validate && changed.some((field) => Object.hasOwn(previous.touched, field) && previous.touched[field])) {
      const validation = validate(values);
      for (const field of fields) {
        if (!Object.hasOwn(previous.touched, field) || !previous.touched[field]) continue;
        delete errors[field];
        if (Object.hasOwn(validation, field) && validation[field] !== undefined) errors[field] = validation[field];
      }
    }

    update({ ...previous, values, errors });
  }

  function setValue<K extends keyof T>(field: K, value: T[K]): void {
    const patch: Partial<T> = {};
    patch[field] = value;
    setValues(patch);
  }

  return {
    values: state.values,
    errors: state.errors,
    touched: state.touched,
    isDirty: fields.some((field) => !Object.is(state.values[field], state.baseline[field])),
    isValid: Object.keys(state.errors).length === 0,
    register: (field) => ({
      name: String(field),
      value: state.values[field],
      onChange: (value) => setValue(field, value),
      onBlur: () => {
        const previous = current.current;
        update({ ...previous, touched: { ...previous.touched, [field]: true } });
      },
    }),
    setValue,
    setValues,
    setErrors: (serverErrors) => {
      const previous = current.current;
      const errors = { ...previous.errors };
      for (const field of fields) {
        if (!Object.hasOwn(serverErrors, field)) continue;
        const message = serverErrors[String(field)]?.[0];
        if (message === undefined) delete errors[field];
        else errors[field] = message;
      }
      update({ ...previous, errors });
    },
    clearError: (field) => {
      const previous = current.current;
      const errors = { ...previous.errors };
      delete errors[field];
      update({ ...previous, errors });
    },
    reset: (values = defaults.current) => {
      update({ values: { ...values }, baseline: { ...values }, errors: {}, touched: {} });
    },
    handleSubmit: (fn) => (event) => {
      event.preventDefault();
      const previous = current.current;
      const validation: Partial<Record<keyof T, string>> = validate?.(previous.values) ?? {};
      const errors = { ...previous.errors };
      const touched = { ...previous.touched };
      for (const field of fields) {
        touched[field] = true;
        if (validate) delete errors[field];
        if (Object.hasOwn(validation, field) && validation[field] !== undefined) errors[field] = validation[field];
      }
      update({ ...previous, errors, touched });
      if (Object.keys(errors).length === 0) fn(previous.values);
    },
  };
}
