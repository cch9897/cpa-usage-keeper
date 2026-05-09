package quota

func mergeHeaders(base map[string]string, overrides map[string]string) map[string]string {
	if len(base) == 0 && len(overrides) == 0 {
		return nil
	}
	headers := make(map[string]string, len(base)+len(overrides))
	for key, value := range base {
		headers[key] = value
	}
	for key, value := range overrides {
		headers[key] = value
	}
	return headers
}
