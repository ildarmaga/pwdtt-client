package backend

type VKLoginStartResult struct {
	URL    string `json:"url"`
	Active bool   `json:"active"`
	Native bool   `json:"native"` // Windows: отдельное окно WebView2 (как WKWebView на iOS)
}

type VKLoginPollResult struct {
	Done    bool   `json:"done"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}
