package workspace

type Unit struct {
	RootPath          string `json:"root_path"`
	Language          string `json:"language"`
	ManifestPath      string `json:"manifest_path"`
	Name              string `json:"name"`
	EnvironmentSource string `json:"environment_source"`
	AdapterID         string `json:"adapter_id"`
}
