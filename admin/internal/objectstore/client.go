package objectstore

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
)

type Entry struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Folder   bool   `json:"folder"`
	Size     int64  `json:"size"`
	Modified string `json:"modified,omitempty"`
}
type Listing struct {
	Prefix  string  `json:"prefix"`
	Entries []Entry `json:"entries"`
	Cursor  string  `json:"cursor,omitempty"`
}
type s3Client struct {
	cfg  Config
	http *http.Client
}

func client(c Config) *s3Client {
	return &s3Client{cfg: c, http: &http.Client{Timeout: 90 * time.Second}}
}
func (c *s3Client) bucketURL() (*url.URL, error) {
	u, err := url.Parse(c.cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, c.cfg.Bucket)
	u.RawPath = ""
	return u, nil
}
func hexHash(b []byte) string { x := sha256.Sum256(b); return hex.EncodeToString(x[:]) }
func hmacSum(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(s))
	return h.Sum(nil)
}
func awsEscape(s string) string {
	r := url.QueryEscape(s)
	r = strings.ReplaceAll(r, "+", "%20")
	r = strings.ReplaceAll(r, "%7E", "~")
	return r
}
func canonicalQuery(v url.Values) string {
	var parts []string
	for k, values := range v {
		for _, x := range values {
			parts = append(parts, awsEscape(k)+"="+awsEscape(x))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "&")
}

func (c *s3Client) request(method, key string, q url.Values, body []byte, headers map[string]string) (*http.Response, error) {
	u, err := c.bucketURL()
	if err != nil {
		return nil, err
	}
	if key != "" {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/" + strings.TrimPrefix(key, "/")
	}
	u.RawQuery = canonicalQuery(q)
	payloadHash := hexHash(body)
	now := time.Now().UTC()
	stamp := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	region := c.cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	h := map[string]string{"host": u.Host, "x-amz-content-sha256": payloadHash, "x-amz-date": stamp}
	for k, v := range headers {
		h[strings.ToLower(k)] = strings.TrimSpace(v)
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var canonical strings.Builder
	for _, k := range keys {
		canonical.WriteString(k + ":" + strings.Join(strings.Fields(h[k]), " ") + "\n")
	}
	signed := strings.Join(keys, ";")
	canonicalURI := u.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	request := method + "\n" + canonicalURI + "\n" + u.RawQuery + "\n" + canonical.String() + "\n" + signed + "\n" + payloadHash
	requestHash := hexHash([]byte(request))
	scope := day + "/" + region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + requestHash
	kDate := hmacSum([]byte("AWS4"+c.cfg.SecretKey), day)
	kRegion := hmacSum(kDate, region)
	kService := hmacSum(kRegion, "s3")
	kSigning := hmacSum(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSum(kSigning, toSign))
	req, err := http.NewRequest(method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range h {
		if k != "host" {
			req.Header.Set(k, v)
		}
	}
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.cfg.AccessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+signature)
	return c.http.Do(req)
}

func (c *s3Client) check(resp *http.Response) error {
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("S3 returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}
func effectivePrefix(c Config, p string) string {
	return strings.Trim(c.Prefix, "/") + func() string {
		if c.Prefix != "" && p != "" {
			return "/"
		}
		return ""
	}() + strings.TrimLeft(p, "/")
}

func (c *s3Client) List(prefix, cursor string) (Listing, error) {
	root := strings.Trim(c.cfg.Prefix, "/")
	if root != "" {
		root += "/"
	}
	full := root + strings.TrimLeft(prefix, "/")
	q := url.Values{"list-type": {"2"}, "delimiter": {"/"}, "prefix": {full}}
	if cursor != "" {
		q.Set("continuation-token", cursor)
	}
	resp, err := c.request(http.MethodGet, "", q, nil, nil)
	if err != nil {
		return Listing{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Listing{}, fmt.Errorf("S3 list failed HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var data struct {
		XMLName  xml.Name `xml:"ListBucketResult"`
		Contents []struct {
			Key      string `xml:"Key"`
			Size     int64  `xml:"Size"`
			Modified string `xml:"LastModified"`
		} `xml:"Contents"`
		Common    []string `xml:"CommonPrefixes>Prefix"`
		Truncated bool     `xml:"IsTruncated"`
		Next      string   `xml:"NextContinuationToken"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&data); err != nil {
		return Listing{}, err
	}
	out := Listing{Prefix: prefix, Entries: []Entry{}}
	for _, x := range data.Contents {
		if x.Key == full {
			continue
		}
		rel := strings.TrimPrefix(x.Key, root)
		out.Entries = append(out.Entries, Entry{Key: rel, Name: path.Base(strings.TrimSuffix(rel, "/")), Size: x.Size, Modified: x.Modified})
	}
	for _, x := range data.Common {
		rel := strings.TrimPrefix(x, root)
		out.Entries = append(out.Entries, Entry{Key: rel, Name: path.Base(strings.TrimSuffix(rel, "/")), Folder: true})
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		if out.Entries[i].Folder != out.Entries[j].Folder {
			return out.Entries[i].Folder
		}
		return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
	})
	if data.Truncated {
		out.Cursor = data.Next
	}
	return out, nil
}

func (c *s3Client) Get(key string) ([]byte, string, error) {
	resp, err := c.Open(key)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return nil, "", err
	}
	if len(b) > 2<<20 {
		return nil, "", errors.New("file exceeds the 2 MB editing limit; download it to edit")
	}
	return b, resp.Header.Get("Content-Type"), nil
}
func (c *s3Client) Open(key string) (*http.Response, error) {
	root := strings.Trim(c.cfg.Prefix, "/")
	if root != "" {
		root += "/"
	}
	resp, err := c.request(http.MethodGet, root+strings.TrimLeft(key, "/"), nil, nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("S3 download failed HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return resp, nil
}
func (c *s3Client) Put(key, contentType string, b []byte) error {
	root := strings.Trim(c.cfg.Prefix, "/")
	if root != "" {
		root += "/"
	}
	h := map[string]string{}
	if contentType != "" {
		h["content-type"] = contentType
	}
	resp, err := c.request(http.MethodPut, root+strings.TrimLeft(key, "/"), nil, b, h)
	if err != nil {
		return err
	}
	return c.check(resp)
}
func (c *s3Client) Delete(key string) error {
	root := strings.Trim(c.cfg.Prefix, "/")
	if root != "" {
		root += "/"
	}
	resp, err := c.request(http.MethodDelete, root+strings.TrimLeft(key, "/"), nil, nil, nil)
	if err != nil {
		return err
	}
	return c.check(resp)
}
func (c *s3Client) DeleteFolder(prefix string) error {
	root := strings.Trim(c.cfg.Prefix, "/")
	if root != "" {
		root += "/"
	}
	full := root + strings.TrimLeft(prefix, "/")
	if !strings.HasSuffix(full, "/") {
		full += "/"
	}
	cursor := ""
	for {
		q := url.Values{"list-type": {"2"}, "prefix": {full}}
		if cursor != "" {
			q.Set("continuation-token", cursor)
		}
		resp, err := c.request(http.MethodGet, "", q, nil, nil)
		if err != nil {
			return err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			return fmt.Errorf("S3 folder listing failed HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
		}
		var page struct {
			Contents []struct {
				Key string `xml:"Key"`
			} `xml:"Contents"`
			Truncated bool   `xml:"IsTruncated"`
			Next      string `xml:"NextContinuationToken"`
		}
		decErr := xml.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&page)
		resp.Body.Close()
		if decErr != nil {
			return decErr
		}
		for _, item := range page.Contents {
			if err := c.Delete(strings.TrimPrefix(item.Key, root)); err != nil {
				return err
			}
		}
		if !page.Truncated {
			break
		}
		cursor = page.Next
		if cursor == "" {
			return errors.New("S3 pagination token missing")
		}
	}
	return nil
}

func List(c Config, prefix, cursor string) (Listing, error) { return client(c).List(prefix, cursor) }
func Get(c Config, key string) ([]byte, string, error)      { return client(c).Get(key) }
func Open(c Config, key string) (*http.Response, error)     { return client(c).Open(key) }
func Put(c Config, key, contentType string, b []byte) error {
	return client(c).Put(key, contentType, b)
}
func Delete(c Config, key string, folder bool) error {
	cl := client(c)
	if folder {
		return cl.DeleteFolder(key)
	}
	return cl.Delete(key)
}
