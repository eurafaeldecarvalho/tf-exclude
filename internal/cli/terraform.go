package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func terraformBin() (string, error) {
	if p := strings.TrimSpace(os.Getenv("TF_EXCLUDE_BIN")); p != "" {
		return p, nil
	}
	p, err := exec.LookPath("terraform")
	if err != nil {
		return "", fmt.Errorf("terraform not found on PATH; set TF_EXCLUDE_BIN to the executable")
	}
	return p, nil
}

type tfVersion struct {
	Major, Minor, Patch int
	Raw                 string
}

func (v tfVersion) String() string {
	if v.Raw != "" {
		return v.Raw
	}
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

func (v tfVersion) atLeast(major, minor int) bool {
	if v.Major != major {
		return v.Major > major
	}
	return v.Minor >= minor
}

var versionRE = regexp.MustCompile(`v?(\d+)\.(\d+)\.(\d+)`)

func parseVersion(s string) (tfVersion, error) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return tfVersion{}, fmt.Errorf("cannot parse terraform version %q", s)
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	return tfVersion{Major: maj, Minor: min, Patch: pat, Raw: m[1] + "." + m[2] + "." + m[3]}, nil
}

func readVersion(bin, chdir string) (tfVersion, error) {
	stdout, stderr, code := runCapture(bin, prefixChdir(chdir, []string{"version", "-json"}), nil)
	if code == 0 {
		var payload struct {
			TerraformVersion string `json:"terraform_version"`
		}
		if err := json.Unmarshal([]byte(stdout), &payload); err == nil && payload.TerraformVersion != "" {
			return parseVersion(payload.TerraformVersion)
		}
	}
	stdout, stderr2, code := runCapture(bin, prefixChdir(chdir, []string{"version"}), nil)
	if code != 0 {
		msg := strings.TrimSpace(stderr + "\n" + stderr2)
		if msg == "" {
			msg = stdout
		}
		return tfVersion{}, fmt.Errorf("terraform version failed: %s", strings.TrimSpace(msg))
	}
	return parseVersion(stdout)
}

func prefixChdir(chdir string, args []string) []string {
	if chdir == "" {
		return args
	}
	return append([]string{"-chdir=" + chdir}, args...)
}

func runCapture(bin string, args []string, extraEnv []string) (string, string, int) {
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := exitCode(err)
	return out.String(), errb.String(), code
}

func runStream(bin string, args []string, stdout, stderr io.Writer) int {
	cmd := exec.Command(bin, args...)
	cmd.Env = os.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return exitCode(cmd.Run())
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}

// backendType reads the backend Terraform recorded at init. An empty string
// means "not initialized" or "unreadable", which is left for terraform itself
// to report. remote and cloud backends execute remotely; -target is not a
// faithful stand-in for -exclude there.
func backendType(chdir string) (string, error) {
	root := chdir
	if root == "" {
		root = "."
	}
	dataDir := os.Getenv("TF_DATA_DIR")
	var meta string
	if dataDir == "" {
		meta = filepath.Join(root, ".terraform", "terraform.tfstate")
	} else if filepath.IsAbs(dataDir) {
		meta = filepath.Join(dataDir, "terraform.tfstate")
	} else {
		meta = filepath.Join(root, dataDir, "terraform.tfstate")
	}
	b, err := os.ReadFile(meta)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var payload struct {
		Backend struct {
			Type string `json:"type"`
		} `json:"backend"`
	}
	if err := json.Unmarshal(b, &payload); err != nil {
		return "", fmt.Errorf("read terraform backend metadata: %w", err)
	}
	return payload.Backend.Type, nil
}

func checkBackend(chdir string) error {
	kind, err := backendType(chdir)
	if err != nil {
		return err
	}
	switch kind {
	case "remote", "cloud":
		return fmt.Errorf("backend %q runs Terraform remotely, which this version of tf-exclude does not support (it only rewrites the local command line)", kind)
	default:
		return nil
	}
}
