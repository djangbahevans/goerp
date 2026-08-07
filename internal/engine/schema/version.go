package schema

func shouldSkipSync(currentVersion, lastSyncedVersion string) bool {
	return currentVersion == lastSyncedVersion
}
