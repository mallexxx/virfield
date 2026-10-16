package tart

import "errors"

var errTartStorageUnsupported = errors.New("local Tart storage requires macOS")

func statVolume(string) (mountedVolume, error) {
	return mountedVolume{}, errTartStorageUnsupported
}

func mountPoint(string) (string, error) {
	return "", errTartStorageUnsupported
}
