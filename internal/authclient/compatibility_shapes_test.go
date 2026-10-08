package authclient

type clientMetadata struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	Platform string `json:"platform"`
}
type credentialFile struct {
	Credentials map[string]Credential `json:"credentials"`
}
