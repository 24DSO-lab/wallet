//go:build !windows

package securestore

func LoadProviderKey(string) (string, error) { return "", ErrUnavailable }
func SaveProviderKey(string, string) error { return ErrUnavailable }
func DeleteProviderKey(string) error { return ErrUnavailable }
