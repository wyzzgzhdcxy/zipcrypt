package archive

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// DefaultPassword 用户指定的默认密码
const DefaultPassword = "wangchaojun"

// configDir 返回配置目录：%APPDATA%\ZipCrypt
func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", err
		}
		base = filepath.Join(home, "AppData", "Roaming")
	}
	dir := filepath.Join(base, "ZipCrypt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func settingsPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

// LoadSettings 读取界面设置，文件不存在时返回默认值
func LoadSettings() Settings {
	s := Settings{
		Password:  DefaultPassword,
		Overwrite: true,
	}
	path, err := settingsPath()
	if err != nil {
		return s
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{Password: DefaultPassword, Overwrite: true}
	}
	if s.Password == "" {
		s.Password = DefaultPassword
	}
	return s
}

// SaveSettings 落盘界面设置（密码按用户要求明文保存在本机配置里）
func SaveSettings(s Settings) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
