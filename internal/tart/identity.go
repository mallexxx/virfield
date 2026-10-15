package tart

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"syscall"
)

// Read hardware identity freshly even when sizing is cached. Display changes,
// serialization and a VM restart do not change it; replacing a same-name VM
// with another ECID/MAC must not inherit a ready lease's authority.
func runtimeIdentity(path string) string {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() < 1 || st.Size() > 1<<20 {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return ""
	}
	var config map[string]json.RawMessage
	if json.Unmarshal(data, &config) != nil {
		return ""
	}
	var mac, model, ecid string
	if json.Unmarshal(config["macAddress"], &mac) != nil || json.Unmarshal(config["hardwareModel"], &model) != nil || model == "" || json.Unmarshal(config["ecid"], &ecid) != nil || ecid == "" || ecid == "0" {
		return ""
	}
	address, err := net.ParseMAC(mac)
	if err != nil || len(address) != 6 {
		return ""
	}
	canonical := map[string]json.RawMessage{}
	for _, key := range []string{"ecid", "hardwareModel", "os", "arch", "version"} {
		canonical[key] = config[key]
	}
	canonical["macAddress"], _ = json.Marshal(address.String())
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return "tart-hardware-v1:" + hex.EncodeToString(digest[:])
}
