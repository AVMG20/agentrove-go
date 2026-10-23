import { useState, useCallback } from 'react';

type FormErrors<T> = Partial<Record<keyof T, string>>;

interface UseAuthFormReturn<T extends Record<string, string>> {
  values: T;
  errors: FormErrors<T> | null;
  setErrors: React.Dispatch<React.SetStateAction<FormErrors<T> | null>>;
  handleChange: (name: keyof T, value: string) => void;
}

export function useAuthForm<T extends Record<string, string>>(
  initialValues: T,
  onMutationReset?: () => void,
): UseAuthFormReturn<T> {
  const [values, setValues] = useState<T>(initialValues);
  const [errors, setErrors] = useState<FormErrors<T> | null>(null);

  const handleChange = useCallback(
    (name: keyof T, value: string) => {
      setValues((prev) => ({ ...prev, [name]: value }));
      setErrors((prev) => {
        if (!prev?.[name]) {
          return prev;
        }

        const rest = { ...prev };
        delete rest[name];
        return Object.keys(rest).length ? rest : null;
      });
      onMutationReset?.();
    },
    [onMutationReset],
  );

  return { values, errors, setErrors, handleChange };
}
