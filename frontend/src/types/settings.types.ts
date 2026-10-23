export type ApiFieldKey = 'github_personal_access_token';

export interface HelperTextLink {
  prefix: string;
  anchorText: string;
  href: string;
}

export interface HelperTextCode {
  prefix: string;
  code: string;
  suffix: string;
}

export interface GeneralSecretFieldConfig {
  key: ApiFieldKey;
  label: string;
  description: string;
  placeholder: string;
  helperText?: HelperTextLink | HelperTextCode;
}
