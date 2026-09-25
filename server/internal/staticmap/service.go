package staticmap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type Pin struct {
	Lat float64
	Lng float64
}
type Service struct {
	Redis  *redis.Client
	Client *http.Client
}

func parsePins(raw string) ([]Pin, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("pins must be a non-empty pipe-separated list of lat,lng pairs")
	}
	seen := map[string]bool{}
	out := []Pin{}
	for _, chunk := range strings.Split(raw, "|") {
		parts := strings.Split(chunk, ",")
		if len(parts) != 2 {
			return nil, errors.New("each pin must be lat,lng")
		}
		lat, e1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		lng, e2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if e1 != nil || e2 != nil {
			return nil, errors.New("each pin must be numeric lat,lng")
		}
		if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
			return nil, errors.New("pin coordinates out of bounds")
		}
		lat = round(lat, 4)
		lng = round(lng, 4)
		key := fmt.Sprintf("%.4f,%.4f", lat, lng)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Pin{lat, lng})
		if len(out) >= 50 {
			break
		}
	}
	if len(out) == 0 {
		return nil, errors.New("pins must contain at least one coordinate pair")
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lat == out[j].Lat {
			return out[i].Lng < out[j].Lng
		}
		return out[i].Lat < out[j].Lat
	})
	return out, nil
}
func round(v float64, n int) float64 {
	p := 1.0
	for i := 0; i < n; i++ {
		p *= 10
	}
	return float64(int(v*p+0.5)) / p
}
func effectiveSize(w, h int) (int, int) {
	f := 1.0
	if x := 640.0 / float64(w); x < f {
		f = x
	}
	if x := 640.0 / float64(h); x < f {
		f = x
	}
	return int(float64(w) * f), int(float64(h) * f)
}
func buildURL(pins []Pin, w, h int, key string) string {
	loc := make([]string, len(pins))
	for i, p := range pins {
		loc[i] = fmt.Sprintf("%.4f,%.4f", p.Lat, p.Lng)
	}
	locations := strings.Join(loc, "%7C")
	framing := ""
	if len(pins) == 1 {
		framing = "center=" + locations + "&zoom=15"
	} else {
		framing = "visible=" + locations
	}
	return "https://maps.googleapis.com/maps/api/staticmap?" + framing + "&size=" + strconv.Itoa(w) + "x" + strconv.Itoa(h) + "&scale=2&format=png&markers=color:0xFFFFFF%7C" + locations + "&style=element:geometry%7Ccolor:0x1a1a1a&style=feature:road%7Celement:geometry%7Ccolor:0x2c2c2c&key=" + url.QueryEscape(key)
}
func (s Service) Fetch(ctx context.Context, pins []Pin, w, h int) ([]byte, error) {
	w, h = effectiveSize(w, h)
	payload := fmt.Sprintf("%v:%d:%d", pins, w, h)
	sum := sha256.Sum256([]byte(payload))
	cacheKey := "staticmap:" + hex.EncodeToString(sum[:])
	if s.Redis != nil {
		if b, e := s.Redis.Get(ctx, cacheKey).Bytes(); e == nil && len(b) > 0 {
			return b, nil
		}
	}
	key := os.Getenv("GOOGLE_MAPS_API_KEY")
	if key == "" {
		return nil, errors.New("GOOGLE_MAPS_API_KEY is not configured")
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, e := client.Get(buildURL(pins, w, h, key))
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") || resp.Header.Get("X-Staticmap-API-Warning") != "" {
		return nil, fmt.Errorf("static map provider returned %d", resp.StatusCode)
	}
	body, e := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if e != nil {
		return nil, e
	}
	if s.Redis != nil {
		_ = s.Redis.Set(ctx, cacheKey, body, 7*24*time.Hour).Err()
	}
	return body, nil
}
