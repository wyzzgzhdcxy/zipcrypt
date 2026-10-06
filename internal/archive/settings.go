package archive

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ArchivePassword 固定的加密密码，硬编码在程序里，界面上不可修改。
// 需求如此：不提供输入框，也不落盘到配置文件。
const ArchivePassword = "fdshainewqjfnsa231!23219jh"

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
	s := Settings{Overwrite: true}
	path, err := settingsPath()
	if err != nil {
		return s
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{Overwrite: true}
	}
	return s
}

// SaveSettings 落盘界面设置（密码是硬编码常量，不存这里）
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
