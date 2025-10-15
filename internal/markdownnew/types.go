// Package markdownnew implements the markdown.new content extraction API.
package markdownnew

const DEFAULT_BASE_URL = "https://markdown.new/"

// ContentRequest identifies a public URL to retrieve through markdown.new.
type ContentRequest struct {
	URL string
}

// ContentResponse is a normalized markdown.new response.
type ContentResponse struct {
	Content         string
	SourceMediaType *string
}
