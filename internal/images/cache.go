package images

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

func isIntegrityError(err error) bool {
	var e *domain.Error
	return errors.As(err, &e) && e.Code == "download_integrity"
}

// quarantineCache keeps bad bytes for inspection while allowing a fresh download.
func quarantineCache(path string) error {
	for i := range 10 {
		name := fmt.Sprintf("%s.corrupt-%d-%d", path, time.Now().UnixNano(), i)
		if _, err := os.Lstat(name); os.IsNotExist(err) {
			return os.Rename(path, name)
		} else if err != nil {
			return err
		}
	}
	return domain.Err("unsafe_cache", "Cannot reserve a quarantine path for corrupt download")
}
