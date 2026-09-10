package generate

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
)

var xmlHeader = []byte(xml.Header)

// marshalXML renders v with a standard XML declaration and indented body.
func marshalXML(v any) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := buf.Write(xmlHeader); err != nil {
		return nil, err
	}
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encode xml: %w", err)
	}
	if err := enc.Flush(); err != nil {
		return nil, fmt.Errorf("flush xml: %w", err)
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// marshalJSON renders v as indented JSON with HTML-escaped output disabled, so
// URLs in the search index stay readable rather than becoming & sequences.
func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encode json: %w", err)
	}
	return buf.Bytes(), nil
}

// errNoBaseURL explains the one configuration mistake that cannot be worked
// around: feeds and sitemaps must contain absolute URLs.
func errNoBaseURL(what string) error {
	return errors.New("cannot generate " + what + ": base_url is not set, and " + what + " requires absolute URLs")
}
