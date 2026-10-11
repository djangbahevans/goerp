# Module declaration validation

The canonical rules are in `nexus-docs/docs/manifest-spec.md`, section 28.
`manifest.Load` enforces rules that need only JSON declarations. `LoadModule`
checks guest exports and discovered routes/models after compilation and temporary
instantiation. `ValidateModuleSet` checks the complete loaded module set before
publication, propagates failures through hard dependencies, and refreshes warnings
against surviving declarations.

Install and reload use `ValidateCandidate` before tenant DDL and repeat validation
under the registry publication lock. Published modules are copied for validation;
a rejected candidate cannot change their status, diagnostics or resolved config.
A replacement that breaks an existing dependent fails publication. Successful
publication retains refreshed warnings on the copied module metadata.

`buildinfo.Version` is `dev` for an unversioned build. Such builds validate engine
range syntax; matching starts when a binary carries a semantic version. Release
builds set the version with `-ldflags '-X
github.com/djangbahevans/goerp/internal/engine/buildinfo.Version=<semver>'`.
Health and schema metadata report this same version.

## Hard-error coverage

Paths in the table are relative to `internal/engine`. The test column identifies
negative coverage at a manifest, discovery, load or route registration entry point.

| Canonical rule | Enforcement | Negative coverage |
| --- | --- | --- |
| Module name format | `manifest/validators.go` | `manifest.TestLoadSupportedDeclarationErrors/invalid_name` |
| Reserved `engine` name | `manifest/validators.go` | `manifest.TestLoadManifest_ReservedEngineName_Rejected` |
| Semantic module version | `manifest/validators.go` | `manifest.TestLoadSupportedDeclarationErrors/invalid_version` |
| Supported ABI version | `manifest/validate_declarations.go` | `manifest.TestLoadSupportedDeclarationErrors/unsupported_ABI` |
| Engine compatibility range | `manifest/compatibility.go`, `loader.LoadModule` | `TestLoadModuleRejectsIncompatibleEngineRelease`; `dev` is unversioned |
| Hard dependencies loaded | `loader/validation.go` | `TestLoadAllSupportedCrossModuleErrors/missing_dependency` |
| Conflicting modules | `loader/validation.go` | `TestLoadAllSupportedCrossModuleErrors/conflict`, `reverse_conflict` |
| Expanded route collisions | `route.RegisterRoutes`, `route.RegisterModuleRoutes` | `TestLoadAllDiscoveredDeclarationErrors/duplicate_routes`, `route.TestRegisterModuleRoutes_CrossCallCollisionErrorsWithoutOverwriting` |
| Permission name collisions | `loader.FindPermissionCollision` | `TestLoadAll_PermissionNameCollision_LaterModuleFailsEarlierUnaffected` |
| Permission references declared and allowed | `loader.ValidateUsesPermissions`, `manifest.validatePolicies` | `TestLoadAll_ViewNamingAnUndeclaredPermissionFailsLoad`, `TestLoadAll_UsesPermissions_PolicyScopesADependencyPermission`; `uses_permissions_test.go` covers routes, operations, fields, transitions, navigation and reports |
| Referenced permission owner is a dependency | `loader.ValidateUsesPermissions` | `TestLoadAll_UsesPermissions_PolicyScopesADependencyPermission` |
| Bridge dependency count | `manifest.validateModuleType` | `manifest.TestLoadManifestModuleTypeConstraintsViolations` |
| Emit capability | `manifest/validators.go` | `manifest.TestLoadManifestEmitsRequiresEventEmitCapability` |
| HTTP capability | `manifest/validate_declarations.go` | `manifest.TestLoadSupportedDeclarationErrors/allowlist_without_capability` |
| Webhook content type | `manifest.validateModuleType` | `manifest.TestWebhookContentTypeIsConnectorOnly`, `TestWebhookContentTypeMustBeABareMediaType` |
| Webhook verifier type and uniqueness | `loader.validateWebhookVerifier`, SDK registration | `TestLoadModule_WebhookVerifier_NonConnectorType_Fails`, `TestLoadModule_TwoWebhookVerifiers_Fails` |
| Event subscription emitter | `loader.ValidateEventSubscriptions` | `TestLoadAll_SubscribesToUnknownEventFails`, `TestValidateEventSubscriptions_ExactVersionMustBeEmitted` |
| View extension dependency | `manifest.validateViewExtensions` | `manifest.TestLoadManifest_ExtendsModuleNotDeclaredDependency_Rejected` |
| Report override target | `loader/validation.go` | `TestLoadAllSupportedCrossModuleErrors/missing_report` |
| Model ownership overlap | `loader/validation.go` | `TestLoadAllSupportedCrossModuleErrors/ownership_collision` |
| Expanded reserved route namespaces | `route.RegisterModuleRoutes` | `TestLoadAllExpandedReservedNamespaces`; `route.TestRegisterModuleRoutes_FalsePositiveGuard` and `ConnectorExpansion` cover valid relative paths and the connector exception |
| OAuth provider name collision | `loader/validation.go` | `TestLoadAllSupportedCrossModuleErrors/OAuth_collision` |
| Field extension targets declared | `manifest.validateModuleType`, `modelextension.Validate` | `manifest.TestLoadManifestModuleTypeConstraintsViolations` |
| Workflow names unique | `manifest/validate_declarations.go` | `manifest.TestLoadSupportedDeclarationErrors/duplicate_workflow` |
| Extension model belongs to declared owner | `modelextension.Resolve` | `TestLoadAllExtensionTargetMustBeOwnedByDeclaredModule` |
| Declared data migration export | `loader.callGetDataMigrations` | `TestLoadModuleDeclaredMigrationRequiresExport` |
| Bridge routes forbidden | `loader.validateModuleRoutes` | `TestLoadModule_RouteForbiddenTypes_RejectRoutes` |
| Automation routes forbidden | `loader.validateModuleRoutes` | `TestLoadModule_RouteForbiddenTypes_RejectRoutes` |
| Theme has no WASM | `manifest.validateModuleType` | `manifest.TestLoadManifestModuleTypeConstraintsViolations` |
| Policy combine mode | `manifest.validatePolicies` | `manifest.TestLoadManifest_PolicyCombine` |
| Policy condition syntax and fields | `manifest.validatePolicies`, `loader/model_references.go` | `manifest.TestLoadManifest_PolicyConditionFailsToParse_Rejected`, `TestLoadAllPolicyFieldResolution` |
| Policy name ownership and length | `manifest.validatePolicies` | `manifest.TestLoadManifest_PolicyNameWrongModuleSegment_Rejected`, `PolicyNameNotThreeSegments_Rejected`, `PolicyNameOverMaxBytes_Rejected` |
| Sequence format | `loader.validateSequenceFormats` | `TestLoadAllDiscoveredDeclarationErrors/sequence_format`, `TestValidateSequenceFormats` |
| Delegate target | Unavailable SDK declaration | Deferred until the model SDK exposes `Delegate` |
| Translatable field type and primary key | Unavailable SDK declaration | Deferred until the model SDK exposes `Translatable` |
| Computed dependency paths | `loader/model_references.go` against effective models | `TestLoadAllComputedDependencyResolution`, `TestComputedPathResolution`; same-record and single-hop Many2One/One2Many paths match the SDK reference |

## Warning coverage

Diagnostics have stable `rule`, `severity`, `module`, `message` and declaration
`context` fields. Manifest diagnostics do not enter serialized package manifests.
Loaded-module metadata retains them for the authenticated tenant-admin module list
and detail endpoints. Missing section diagnostics skip the extension; warnings do
not independently fail module loading.

| Canonical warning | Rule identity | Coverage |
| --- | --- | --- |
| Soft dependency event has no matching emitter | `subscribes.missing_emitter` | `TestLoadAllRetainsLocalAndCrossModuleWarnings`, `TestValidateCandidateRefreshesWarningsWithoutMutatingPublishedModules` |
| Unknown default role | `default_roles.unknown` | `manifest.TestLoadWarningDiagnostics/unknown_role`, `TestLoadAllRetainsLocalAndCrossModuleWarnings` |
| Missing extension section | `view_extensions.missing_section` | `TestLoadAll_ViewExtensionTargetSectionMissing_WarnsAndLoads` |
| Config key has no seed | `config_schema.missing_seed` | `manifest.TestLoadWarningDiagnostics`; required/optional keys, qualified keys, false and null seeds |
| Description exceeds 4096 characters | `long_description.length` | `manifest.TestLoadWarningDiagnostics`; Unicode character counting and boundary |
| HTTP allowlist URL | `http_allowlist.insecure` | `manifest.TestLoadWarningDiagnostics/HTTP_allowed_with_warning`, `TestLoadAllRetainsLocalAndCrossModuleWarnings` |

The permission-owner warning uses `uses_permissions.missing_owner` and preserves
the deny behavior for unavailable soft dependencies. `adminmodules.TestModuleQueriesExposeDiagnostics`
checks diagnostics through both authenticated HTTP query handlers.
