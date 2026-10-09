declare module "virtual:goerp-module-development" {
  export const developmentModuleName: string | null;
  export function loadDevelopmentModule(): Promise<unknown>;
}
