package chatfiles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"remi/server/ent"
	"remi/server/ent/chatfile"
	"remi/server/ent/user"
)

const MaxPartSize = 50 * 1024 * 1024

type File struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Thumbnail    string    `json:"thumbnail,omitempty"`
	MimeType     string    `json:"mime_type"`
	OpenAIFileID string    `json:"openai_file_id"`
	CreatedAt    time.Time `json:"created_at"`
}
type Service struct {
	Client         *ent.Client
	HTTP           *http.Client
	Endpoint       string
	APIKey         string
	ThumbnailStore ThumbnailStore
}

func thumbnailBytes(data []byte, mime string) ([]byte, error) {
	if !strings.HasPrefix(strings.ToLower(mime), "image/") {
		return nil, errors.New("not an image")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	scale := 1.0
	if width > 128 || height > 128 {
		if width > height {
			scale = 128 / float64(width)
		} else {
			scale = 128 / float64(height)
		}
	}
	nw, nh := int(float64(width)*scale), int(float64(height)*scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	thumb := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			sx := bounds.Min.X + x*width/nw
			sy := bounds.Min.Y + y*height/nh
			thumb.Set(x, y, img.At(sx, sy))
		}
	}
	var out bytes.Buffer
	if strings.Contains(strings.ToLower(mime), "jpeg") || strings.Contains(strings.ToLower(mime), "jpg") {
		err = jpeg.Encode(&out, thumb, &jpeg.Options{Quality: 85})
	} else {
		err = png.Encode(&out, thumb)
	}
	if err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func writeThumbnail(uid, name string, data []byte) string {
	root, public := os.Getenv("CHAT_FILES_DIR"), strings.TrimRight(os.Getenv("CHAT_FILES_PUBLIC_URL"), "/")
	if root == "" || public == "" {
		return ""
	}
	safeUID := fmt.Sprintf("%x", sha256.Sum256([]byte(uid)))[:32]
	safeName := filepath.Base(name)
	dir := filepath.Join(root, safeUID)
	if os.MkdirAll(dir, 0750) != nil {
		return ""
	}
	if os.WriteFile(filepath.Join(dir, safeName), data, 0640) != nil {
		return ""
	}
	return public + "/" + safeUID + "/" + url.PathEscape(safeName)
}

func validateUpload(name, mime string, size int64) error {
	if size > MaxPartSize {
		return errors.New("file exceeds 50 MB limit")
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if strings.HasPrefix(strings.ToLower(mime), "image/") {
		switch ext {
		case "png", "jpg", "jpeg", "gif", "webp":
			return nil
		}
	}
	allowed := map[string]bool{"pdf": true, "txt": true, "md": true, "csv": true, "tsv": true, "json": true, "xml": true, "html": true, "htm": true, "doc": true, "docx": true, "rtf": true, "odt": true, "xls": true, "xlsx": true, "ppt": true, "pptx": true, "py": true, "go": true, "js": true, "ts": true, "css": true, "sql": true, "yaml": true, "yml": true, "toml": true, "log": true}
	if !allowed[ext] {
		return errors.New("unsupported chat file type")
	}
	return nil
}
func (s Service) owner(ctx context.Context, uid string) (*ent.User, error) {
	u, e := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(e) {
		return s.Client.User.Create().SetExternalUID(uid).SetEmail(uid).SetName(uid).Save(ctx)
	}
	return u, e
}
func (s Service) Upload(ctx context.Context, uid, name, mime string, data []byte) (File, error) {
	if err := validateUpload(name, mime, int64(len(data))); err != nil {
		return File{}, err
	}
	u, err := s.owner(ctx, uid)
	if err != nil {
		return File{}, err
	}
	thumbnailURL := ""
	if strings.HasPrefix(strings.ToLower(mime), "image/") {
		thumb, thumbErr := thumbnailBytes(data, mime)
		if thumbErr != nil {
			return File{}, fmt.Errorf("unsupported image: %w", thumbErr)
		}
		stem := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
		ext := strings.TrimPrefix(filepath.Ext(name), ".")
		thumbnailName := stem + "_thumbnail." + ext
		if s.ThumbnailStore != nil {
			thumbnailURL, _ = s.ThumbnailStore.Put(ctx, uid, thumbnailName, mime, thumb)
		} else {
			thumbnailURL = writeThumbnail(uid, thumbnailName, thumb)
		}
	}
	key := s.APIKey
	if key == "" {
		key = os.Getenv("OPENAI_API_KEY")
	}
	if key == "" {
		return File{}, errors.New("chat file provider is not configured")
	}
	endpoint := s.Endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/files"
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("purpose", "user_data")
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filepath.Base(name)))
	partHeader.Set("Content-Type", mime)
	part, _ := mw.CreatePart(partHeader)
	_, _ = part.Write(data)
	_ = mw.Close()
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if e != nil {
		return File{}, e
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, e := client.Do(req)
	if e != nil {
		return File{}, e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return File{}, fmt.Errorf("file provider returned %d", resp.StatusCode)
	}
	var provider struct {
		ID   string `json:"id"`
		Name string `json:"filename"`
	}
	if json.NewDecoder(resp.Body).Decode(&provider) != nil || provider.ID == "" {
		return File{}, errors.New("invalid file provider response")
	}
	id := uuid.NewString()
	if provider.Name == "" {
		provider.Name = filepath.Base(name)
	}
	nodeBuilder := s.Client.ChatFile.Create().SetExternalID(id).SetName(provider.Name).SetMimeType(mime).SetOpenaiFileID(provider.ID).SetUserID(u.ID)
	if thumbnailURL != "" {
		nodeBuilder.SetThumbnail(thumbnailURL)
	}
	node, e := nodeBuilder.Save(ctx)
	if e != nil {
		return File{}, e
	}
	return File{ID: node.ExternalID, Name: node.Name, Thumbnail: node.Thumbnail, MimeType: node.MimeType, OpenAIFileID: node.OpenaiFileID, CreatedAt: node.CreatedAt}, nil
}
func (s Service) List(ctx context.Context, uid string) ([]File, error) {
	u, e := s.owner(ctx, uid)
	if e != nil {
		return nil, e
	}
	nodes, e := s.Client.ChatFile.Query().Where(chatfile.HasUserWith(user.IDEQ(u.ID))).Order(ent.Desc(chatfile.FieldCreatedAt)).All(ctx)
	if e != nil {
		return nil, e
	}
	out := make([]File, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, File{ID: n.ExternalID, Name: n.Name, Thumbnail: n.Thumbnail, MimeType: n.MimeType, OpenAIFileID: n.OpenaiFileID, CreatedAt: n.CreatedAt})
	}
	return out, nil
}
