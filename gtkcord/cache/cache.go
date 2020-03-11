package cache

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/diamondburned/gtkcord3/gtkcord/gtkutils"
	"github.com/diamondburned/gtkcord3/gtkcord/semaphore"
	"github.com/diamondburned/gtkcord3/log"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/gtk"
	"github.com/pkg/errors"
)

var Client = http.Client{
	Timeout: 15 * time.Second,
}

// DO NOT TOUCH.
const (
	CacheHash   = "trapsaregood"
	CachePrefix = "gtkcord3"
)

var (
	DirName = CachePrefix + "-" + CacheHash
	Temp    = os.TempDir()
	Path    = filepath.Join(Temp, DirName)
)

// var store *diskv.Diskv

func init() {
	cleanUpCache()

	// store = diskv.New(diskv.Options{
	// 	BasePath:          Path,
	// 	AdvancedTransform: TransformURL,
	// 	InverseTransform:  InverseTransformURL,
	// })
}

func cleanUpCache() {
	tmp, err := os.Open(Temp)
	if err != nil {
		return
	}

	dirs, err := tmp.Readdirnames(-1)
	if err != nil {
		return
	}

	for _, d := range dirs {
		if strings.HasPrefix(d, CachePrefix+"-") && d != DirName {
			path := filepath.Join(Temp, d)
			log.Infoln("Deleting", path)
			os.RemoveAll(path)
		}
	}
}

func TransformURL(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return filepath.Join(Path, SanitizeString(s))
	}

	if err := os.MkdirAll(filepath.Join(Path, u.Hostname()), 0755|os.ModeDir); err != nil {
		log.Errorln("Failed to mkdir:", err)
	}

	return filepath.Join(Path, u.Hostname(), SanitizeString(u.EscapedPath()+"?"+u.RawQuery))
}

// SanitizeString makes the string friendly to put into the file system. It
// converts anything that isn't a digit or letter into underscores.
func SanitizeString(str string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '#' {
			return r
		}

		return '_'
	}, str)
}

// func Get(url string) ([]byte, error) {
// 	b, err := get(url)
// 	if err != nil {
// 		return b, err
// 	}

// 	return b, nil
// }

var fileIO sync.Mutex

// get doesn't check if the file exists
func get(url, dst string, pp []Processor) error {
	r, err := Client.Get(url)
	if err != nil {
		return errors.Wrap(err, "Failed to GET")
	}
	defer r.Body.Close()

	if r.StatusCode < 200 || r.StatusCode > 299 {
		return fmt.Errorf("Bad status code %d for %s", r.StatusCode, url)
	}

	b, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return errors.Wrap(err, "Failed to download image")
	}

	if len(b) == 0 {
		return errors.New("nil body")
	}

	if len(pp) > 0 {
		b = Process(b, pp)
	}

	fileIO.Lock()
	defer fileIO.Unlock()

	if err := ioutil.WriteFile(dst, b, 0755); err != nil {
		return errors.Wrap(err, "Failed to write file to "+dst)
	}

	return nil
}

func pixbufFromFile(file string) (*gdk.Pixbuf, error) {
	fileIO.Lock()
	defer fileIO.Unlock()

	return gdk.PixbufNewFromFile(file)
}

func GetPixbuf(url string, pp ...Processor) (*gdk.Pixbuf, error) {
	return GetPixbufScaled(url, 0, 0, pp...)
}

func GetPixbufScaled(url string, w, h int, pp ...Processor) (*gdk.Pixbuf, error) {
	// Transform URL:
	dst := TransformURL(url)

	// Try and get the Pixbuf from file:
	p, err := pixbufFromFile(dst)
	if err == nil {
		return p, nil
	}

	// If resize is requested, we resize using Go's instead.
	if w > 0 && h > 0 {
		pp = Prepend(Resize(w, h), pp)
	}

	// Get the image into file (dst)
	if err := get(url, dst, pp); err != nil {
		return nil, err
	}

	p, err = pixbufFromFile(dst)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to get pixbuf")
	}

	return p, nil
}

func SetImage(url string, img *gtk.Image, pp ...Processor) error {
	return SetImageScaled(url, img, 0, 0, pp...)
}

func SetImageScaled(url string, img *gtk.Image, w, h int, pp ...Processor) error {
	p, err := GetPixbufScaled(url, w, h, pp...)
	if err != nil {
		return err
	}

	semaphore.IdleMust(img.SetFromPixbuf, p)
	return nil
}

// SetImageAsync is not cached.
func SetImageAsync(url string, img *gtk.Image, w, h int) error {
	r, err := Client.Get(url)
	if err != nil {
		return errors.Wrap(err, "Failed to GET "+url)
	}
	defer r.Body.Close()

	if r.StatusCode < 200 || r.StatusCode > 299 {
		return fmt.Errorf("Bad status code %d for %s", r.StatusCode, url)
	}

	var gif = strings.Contains(url, ".gif")
	var l *gdk.PixbufLoader

	l, err = gdk.PixbufLoaderNew()
	if err != nil {
		return errors.Wrap(err, "Failed to create a pixbuf_loader")
	}

	if w > 0 && h > 0 {
		gtkutils.Connect(l, "size-prepared", func(_ interface{}, _w, _h int) {
			w, h = maxSize(_w, _h, w, h)
			l.SetSize(w, h)
		})
	}

	gtkutils.Connect(l, "area-updated", func() {
		if gif {
			semaphore.IdleMust(func() {
				p, err := l.GetAnimation()
				if err != nil || p == nil {
					log.Errorln("Failed to get pixbuf during area-prepared:", err)
					return
				}
				img.SetFromAnimation(p)
			})
		} else {
			semaphore.IdleMust(func() {
				p, err := l.GetPixbuf()
				if err != nil || p == nil {
					log.Errorln("Failed to get pixbuf during area-prepared:", err)
					return
				}
				img.SetFromPixbuf(p)
			})
		}
	})

	if _, err := io.Copy(l, r.Body); err != nil {
		return errors.Wrap(err, "Failed to stream to pixbuf_loader")
	}

	if err := l.Close(); err != nil {
		return errors.Wrap(err, "Failed to close pixbuf_loader")
	}

	return nil
}

func AsyncFetch(url string, img *gtk.Image, w, h int, pp ...Processor) {
	semaphore.IdleMust(gtkutils.ImageSetIcon, img, "image-missing", 24)

	if len(pp) == 0 && w != 0 && h != 0 {
		go func() {
			if err := SetImageAsync(url, img, w, h); err != nil {
				log.Errorln("Failed to get image", url+":", err)
				return
			}
		}()

	} else {
		go func() {
			if err := SetImageScaled(url, img, w, h, pp...); err != nil {
				log.Errorln("Failed to get image", url+":", err)
				return
			}
		}()
	}
}

func maxSize(w, h, maxW, maxH int) (int, int) {
	if w > h {
		h = h * maxW / w
		w = maxW
	} else {
		w = w * maxH / h
		h = maxH
	}

	return w, h
}
