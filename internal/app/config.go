package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

// Default credentials for the public nethack.alt.org SSH gateway. The actual
// per-player game account is entered later inside dgamelaunch, NOT here.
const (
	defaultSSHUser     = "nethack"
	defaultSSHPassword = ""
)

// Config holds credentials persisted next to the binary.
type Config struct {
	// SSHUser is the SSH-layer username for the game server's public gateway.
	// For nethack.alt.org this is "nethack" (the shared public account).
	// Leave empty to fall back to defaultSSHUser.
	SSHUser string `json:"ssh_user"`
	// SSHPassword is the SSH-layer password for the public gateway. For
	// alt.org it is empty / accepted-any. This is NOT your alt.org game
	// account password — that one is entered inside the dgamelaunch menu
	// after the SSH session connects.
	SSHPassword      string `json:"ssh_password"`
	OpenRouterAPIKey string `json:"openrouter_api_key"`
	// Model is the OpenRouter / OpenAI-compatible model slug used for
	// translation. Empty falls back to defaultModel.
	Model string `json:"model,omitempty"`
	// BaseURL overrides the chat-completions endpoint. Empty falls back to
	// defaultBaseURL (OpenRouter). Must point at the full /chat/completions
	// path of an OpenAI-compatible API.
	BaseURL string `json:"base_url,omitempty"`
	// FastModel is a cheap / low-latency model used for short classification
	// tasks (e.g. "is this popup a menu or narrative?"). Defaults to
	// defaultFastModel when empty. Cost-wise it's roughly an order of
	// magnitude cheaper than the main translation model.
	FastModel string `json:"fast_model,omitempty"`
	// FastBaseURL / FastAPIKey are optional — only set them if the fast
	// model lives on a different provider. Empty values fall back to
	// BaseURL / OpenRouterAPIKey.
	FastBaseURL string `json:"fast_base_url,omitempty"`
	FastAPIKey  string `json:"fast_api_key,omitempty"`
}

// configPath returns the absolute path to config.json sitting next to the
// running executable. Symlinks are resolved so a brew-installed shim still
// points back at the real install dir.
func configPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Join(filepath.Dir(exe), "config.json"), nil
}

// loadConfig reads config.json, prompting for credentials on first launch.
func loadConfig() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err == nil {
		var cfg Config
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if cfg.OpenRouterAPIKey == "" {
			return nil, fmt.Errorf("config %s is missing openrouter_api_key", path)
		}
		applyConfigDefaults(&cfg)
		return &cfg, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	cfg, err := promptForConfig()
	if err != nil {
		return nil, err
	}
	if err := saveConfig(path, cfg); err != nil {
		return nil, err
	}
	applyConfigDefaults(cfg)
	fmt.Printf("Configuration saved to %s\n", path)
	return cfg, nil
}

// applyConfigDefaults fills in runtime defaults for optional fields so old
// config.json files (written before these knobs existed) keep working.
func applyConfigDefaults(cfg *Config) {
	if cfg.SSHUser == "" {
		cfg.SSHUser = defaultSSHUser
	}
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.FastModel == "" {
		cfg.FastModel = defaultFastModel
	}
	if cfg.FastBaseURL == "" {
		cfg.FastBaseURL = cfg.BaseURL
	}
	if cfg.FastAPIKey == "" {
		cfg.FastAPIKey = cfg.OpenRouterAPIKey
	}
}

func saveConfig(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func promptForConfig() (*Config, error) {
	fmt.Println("First-time setup for nh-helper.")
	fmt.Println("Credentials will be stored locally in config.json (chmod 0600).")
	fmt.Println()
	fmt.Println("The SSH user/password are for the public game-server gateway,")
	fmt.Println("NOT your personal alt.org game account. For nethack.alt.org,")
	fmt.Println("just press Enter at both SSH prompts to accept the defaults.")
	fmt.Println("Your alt.org game account is entered later, inside the")
	fmt.Println("dgamelaunch menu that appears after connecting.")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)

	user, err := promptLine(reader, fmt.Sprintf("SSH Username [%s]: ", defaultSSHUser))
	if err != nil {
		return nil, err
	}

	password, err := promptSecret("SSH Password (blank for alt.org): ")
	if err != nil {
		return nil, err
	}

	apiKey, err := promptSecret("OpenRouter API Key: ")
	if err != nil {
		return nil, err
	}
	if apiKey == "" {
		return nil, errors.New("OpenRouter API key cannot be empty")
	}

	model, err := promptLine(reader, fmt.Sprintf("Model slug [%s]: ", defaultModel))
	if err != nil {
		return nil, err
	}

	return &Config{
		SSHUser:          user,
		SSHPassword:      password,
		OpenRouterAPIKey: apiKey,
		Model:            model,
	}, nil
}

func promptLine(r *bufio.Reader, label string) (string, error) {
	fmt.Print(label)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func promptSecret(label string) (string, error) {
	fmt.Print(label)
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		// Fallback: read plaintext line (e.g. when piping).
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}
	raw, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}
