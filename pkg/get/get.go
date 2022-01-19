package get

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/alexellis/arkade/pkg/env"
)

var supportedOS = [...]string{"linux", "darwin", "ming"}
var supportedArchitectures = [...]string{"x86_64", "arm", "amd64", "armv6l", "armv7l", "arm64", "aarch64"}

// Strings used to autodetect archive compatibility with os and arch
// OS Strings
var linuxStrings = []string{"linux"}
var windowsStrings = []string{"windows", ".exe"}
var macStrings = []string{"osx", "darwin", "mac"}

// Arch strings
var arch32bitStrings = []string{"linux32", "win32"}
var arch64bitStrings = []string{"amd64", "linux64", "win64"}
var archARM7Strings = []string{"armv7", "arm-v7", "arm"}
var archARM64Strings = []string{"arm64"}

// Tool describes how to download a CLI tool from a binary
// release - whether a single binary, or an archive.
type Tool struct {
	// The name of the tool for download
	Name string

	// Repo is a GitHub repo, when no repo exists, use the same
	// as the name.
	Repo string

	// Owner is the name of the GitHub account, when no account
	// exists, use the vendor name lowercase.
	Owner string

	// Version pinned or left empty to pull the latest release
	// if any only if only BinaryTemplate is specified.
	Version string

	// Description of what the tool is used for.
	Description string

	// URLTemplate specifies a Go template for the download URL
	// override the OS, architecture and extension
	// All whitespace will be trimmed/
	URLTemplate string

	// The binary template can be used when downloading GitHub
	// It assumes that the only part of the URL needing to be
	// templated is the binary name on a standard GitHub download
	// URL.
	BinaryTemplate string

	// NoExtension is required for tooling such as kubectx
	// which at time of writing is a bash script.
	NoExtension bool
}

type ToolLocal struct {
	Name string
	Path string
}

var templateFuncs = map[string]interface{}{
	"HasPrefix": func(s, prefix string) bool { return strings.HasPrefix(s, prefix) },
}

func (tool Tool) IsArchive() (bool, error) {
	arch, operatingSystem := env.GetClientArch()
	version := ""

	downloadURL, version, err := GetDownloadURL(&tool, strings.ToLower(operatingSystem), strings.ToLower(arch), version)
	if err != nil {
		return false, err
	}

	return strings.HasSuffix(downloadURL, "tar.gz") ||
		strings.HasSuffix(downloadURL, "zip") ||
		strings.HasSuffix(downloadURL, "tgz"), nil
}

func isOsCompatible(release, os string) bool {
	var incompatibleStrings []string
	var osStrings []string

	if strings.Contains(os, "linux") {
		osStrings = linuxStrings
		incompatibleStrings = append(windowsStrings, macStrings...)
	} else if strings.Contains(os, "darwin") {
		osStrings = macStrings
		incompatibleStrings = append(linuxStrings, windowsStrings...)
	} else if strings.Contains(os, "mingw") {
		osStrings = windowsStrings
		incompatibleStrings = append(linuxStrings, macStrings...)
	} else {
		return false
	}
	// Exclude release for non-compatible os
	for _, excludeString := range incompatibleStrings {
		if strings.Contains(release, excludeString) {
			return false
		}
	}
	// Check if something is compatible for us
	for _, osString := range osStrings {
		if strings.Contains(release, osString) {
			return true
		}
	}
	// If we are here, the release is incompatible ...
	return false
}

func isArchCompatible(release, arch string) bool {
	var incompatibleStrings []string
	var archStrings []string

	if strings.Contains(arch, "i686") {
		archStrings = arch32bitStrings
		incompatibleStrings = append(arch64bitStrings, archARM7Strings...)
		incompatibleStrings = append(incompatibleStrings, archARM64Strings...)
	} else if strings.Contains(arch, "x86_64") {
		archStrings = arch64bitStrings
		incompatibleStrings = append(arch32bitStrings, archARM7Strings...)
		incompatibleStrings = append(incompatibleStrings, archARM64Strings...)
	} else if strings.Contains(arch, "armv7l") {
		archStrings = archARM7Strings
		incompatibleStrings = append(arch32bitStrings, arch64bitStrings...)
		incompatibleStrings = append(incompatibleStrings, archARM64Strings...)
	} else if strings.Contains(arch, "aarch64") {
		archStrings = archARM7Strings
		incompatibleStrings = append(arch32bitStrings, arch64bitStrings...)
		incompatibleStrings = append(incompatibleStrings, archARM64Strings...)
	} else {
		log.Printf("Cannot handle arch %s", arch)
	}
	// Exclude release for non-compatible os
	for _, excludeString := range incompatibleStrings {
		if strings.Contains(release, excludeString) {
			return false
		}
	}
	// Check if something is compatible for us
	for _, osString := range archStrings {
		if strings.Contains(release, osString) {
			return true
		}
	}
	return false
}

func (tool Tool) getGithubReleaseArchive(os, arch, version string) (string, error) {

	archives := make([]string, 0)

	url := fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s", tool.Owner, tool.Repo, version)

	timeout := time.Second * 5
	client := makeHTTPClient(&timeout, false)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	res, err := client.Do(req)
	if err != nil {
		return "", err
	}

	if res.Body != nil {
		defer res.Body.Close()
	}

	if res.StatusCode != 200 {
		return "", fmt.Errorf("incorrect status code: %d", res.StatusCode)
	}

	expr := fmt.Sprintf(".*href=\"(/%s/%s/releases/download/[^\"]+)\".*", tool.Owner, tool.Repo)
	re := regexp.MustCompile(expr)

	buffer := bufio.NewScanner(res.Body)
	for buffer.Scan() {
		if re.Match(buffer.Bytes()) {
			context := re.ReplaceAllString(buffer.Text(), "$1")
			if !isOsCompatible(context, os) || !isArchCompatible(context, arch) {
				continue
			}
			fmt.Printf("Archive %s seems to be compatible with %s/%s\n", context, os, arch)
			archives = append(archives, fmt.Sprintf("https://github.com%s", context))
		}
	}
	if len(archives) == 0 {
		return "", fmt.Errorf(
			"no release found for %s on os(%s)/arch(%s) using version %s", tool.Name, os, arch, version)
	} else if len(archives) > 1 {
		return "", fmt.Errorf(
			"found multiple release for %s on os(%s)/arch(%s) using version %s", tool.Name, os, arch, version)
	}
	return archives[0], nil
}

// GetDownloadURL fetches the download URL for a release of a tool
// for a given os, architecture and version
func GetDownloadURL(tool *Tool, os, arch, version string) (string, string, error) {
	ver := getToolVersion(tool, version)

	dlURL, ver, err := tool.GetURL(os, arch, ver)
	if err != nil {
		return "", "", err
	}

	return dlURL, ver, nil
}

func getToolVersion(tool *Tool, version string) string {
	ver := tool.Version
	if len(version) > 0 {
		ver = version
	}

	return ver
}

func (tool Tool) GetURL(os, arch, version string) (string, string, error) {

	if strings.Compare(version, "latest") == 0 || len(version) == 0 &&
		(len(tool.URLTemplate) == 0 || strings.Contains(tool.URLTemplate, "https://github.com/")) {
		log.Printf("Looking up version for %s", tool.Name)
		v, err := findGitHubRelease(tool.Owner, tool.Repo)
		if err != nil {
			return "", "", err
		}
		log.Printf("Found: %s", v)
		tool.Version = v
		version = v
	}

	if len(tool.URLTemplate) > 0 {
		return getByDownloadTemplate(tool, os, arch, version)
	}

	return getURLByGithubTemplate(tool, os, arch, version)
}

func getURLByGithubTemplate(tool Tool, os, arch, version string) (string, string, error) {

	var err error
	t := template.New(tool.Name + "binary")
	t = t.Funcs(templateFuncs)
	t, err = t.Parse(tool.BinaryTemplate)
	if err != nil {
		return "", "", err
	}

	var buf bytes.Buffer
	pref := map[string]string{
		"OS":            os,
		"Arch":          arch,
		"Name":          tool.Name,
		"Version":       version,
		"VersionNumber": strings.TrimPrefix(version, "v"),
	}

	err = t.Execute(&buf, pref)
	if err != nil {
		return "", "", err
	}

	downloadName := strings.TrimSpace(buf.String())

	return getBinaryURL(tool.Owner, tool.Repo, version, downloadName), version, nil
}

func findGitHubRelease(owner, repo string) (string, error) {
	url := fmt.Sprintf("https://github.com/%s/%s/releases/latest", owner, repo)

	timeout := time.Second * 5
	client := makeHTTPClient(&timeout, false)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	req, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		return "", err
	}

	res, err := client.Do(req)
	if err != nil {
		return "", err
	}

	if res.Body != nil {
		defer res.Body.Close()
	}

	if res.StatusCode != 302 {
		return "", fmt.Errorf("incorrect status code: %d", res.StatusCode)
	}

	loc := res.Header.Get("Location")
	if len(loc) == 0 {
		return "", fmt.Errorf("unable to determine release of tool")
	}

	version := loc[strings.LastIndex(loc, "/")+1:]
	return version, nil
}

func getBinaryURL(owner, repo, version, downloadName string) string {
	if in := strings.Index(downloadName, "/"); in > -1 {
		return fmt.Sprintf(
			"https://github.com/%s/%s/releases/download/%s",
			owner, repo, downloadName)
	}
	return fmt.Sprintf(
		"https://github.com/%s/%s/releases/download/%s/%s",
		owner, repo, version, downloadName)
}

func getByDownloadTemplate(tool Tool, os, arch, version string) (string, string, error) {
	var err error
	t := template.New(tool.Name)
	t = t.Funcs(templateFuncs)
	t, err = t.Parse(tool.URLTemplate)
	if err != nil {
		return "", "", err
	}

	var buf bytes.Buffer
	inputs := map[string]string{
		"OS":            os,
		"Arch":          arch,
		"Version":       version,
		"VersionNumber": strings.TrimPrefix(version, "v"),
		"Repo":          tool.Repo,
		"Owner":         tool.Owner,
		"Name":          tool.Name,
	}

	if err := t.Execute(&buf, inputs); err != nil {
		return "", "", err
	}

	res := strings.TrimSpace(buf.String())
	return res, version, nil
}

// makeHTTPClient makes a HTTP client with good defaults for timeouts.
func makeHTTPClient(timeout *time.Duration, tlsInsecure bool) http.Client {
	return makeHTTPClientWithDisableKeepAlives(timeout, tlsInsecure, false)
}

// makeHTTPClientWithDisableKeepAlives makes a HTTP client with good defaults for timeouts.
func makeHTTPClientWithDisableKeepAlives(timeout *time.Duration, tlsInsecure bool, disableKeepAlives bool) http.Client {
	client := http.Client{}

	if timeout != nil || tlsInsecure {
		tr := &http.Transport{
			Proxy:             http.ProxyFromEnvironment,
			DisableKeepAlives: disableKeepAlives,
		}

		if timeout != nil {
			client.Timeout = *timeout
			tr.DialContext = (&net.Dialer{
				Timeout: *timeout,
			}).DialContext

			tr.IdleConnTimeout = 120 * time.Millisecond
			tr.ExpectContinueTimeout = 1500 * time.Millisecond
		}

		if tlsInsecure {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: tlsInsecure}
		}

		tr.DisableKeepAlives = disableKeepAlives

		client.Transport = tr
	}

	return client
}

// GetBinaryName returns the name of a binary for the given tool or an
// error if the tool's template cannot be parsed or executed.
func GetBinaryName(tool *Tool, os, arch, version string) (string, error) {
	if len(tool.BinaryTemplate) > 0 {
		var err error
		t := template.New(tool.Name + "_binaryname")
		t = t.Funcs(templateFuncs)

		t, err = t.Parse(tool.BinaryTemplate)
		if err != nil {
			return "", err
		}

		var buf bytes.Buffer
		ver := getToolVersion(tool, version)
		if err := t.Execute(&buf, map[string]string{
			"OS":            os,
			"Arch":          arch,
			"Name":          tool.Name,
			"Version":       ver,
			"VersionNumber": strings.TrimPrefix(ver, "v"),
		}); err != nil {
			return "", err
		}

		res := strings.TrimSpace(buf.String())
		return res, nil
	}

	return "", errors.New("BinaryTemplate is not set")
}

// GetDownloadURLs generates a list of URL for each tool, for download
func GetDownloadURLs(tools Tools, toolArgs []string, version string) (Tools, error) {
	arkadeTools := []Tool{}

	for _, arg := range toolArgs {
		name := arg

		// Handle version specified tool name
		if i := strings.LastIndex(arg, "@"); i > -1 {
			name = arg[:i]
			if len(version) > 0 {
				return nil, fmt.Errorf("cannot specify --version flag and @ syntax at the same time for %s", name)
			}
			version = arg[i+1:]
		}

		err := toolExists(&arkadeTools, tools, name, version)
		if err != nil {
			return nil, err
		}

		// unset value for the next iteration of versioned tool
		version = ""
	}

	return arkadeTools, nil
}

// toolExists checks if user provided tool exists on arkade
func toolExists(arkadeTools *[]Tool, tools Tools, name, version string) error {
	for _, tool := range tools {
		if name == tool.Name {
			if len(version) > 0 {
				tool.Version = version
			}
			*arkadeTools = append(*arkadeTools, tool)

			return nil
		}
	}
	return fmt.Errorf("tool %s not found", name)
}

// PostInstallationMsg generates installation message after tool has been downloaded
func PostInstallationMsg(dlMode int, localToolsStore []ToolLocal) ([]byte, error) {

	t := template.New("Installation Instructions")

	if dlMode == DownloadTempDir {
		t.Parse(`Run the following to copy to install the tool:

chmod +x {{range .}}{{.Path}} {{end}}
{{- range . }}
sudo install -m 755 {{.Path}} /usr/local/bin/{{.Name}}
{{- end}}`)

	} else {
		t.Parse(`# Add arkade binary directory to your PATH variable
export PATH=$PATH:$HOME/.arkade/bin/

# Test the binary:
{{- range . }}
{{.Path}}
{{- end }}

# Or install with:
{{- range . }}
sudo mv {{.Path}} /usr/local/bin/

{{- end}}`)
	}

	var tpl bytes.Buffer

	err := t.Execute(&tpl, localToolsStore)
	if err != nil {
		return nil, err
	}

	return tpl.Bytes(), err
}

// ValidateOS returns whether a given operating system is supported
func ValidateOS(name string) error {
	for _, os := range supportedOS {
		if strings.HasPrefix(strings.ToLower(name), os) {
			return nil
		}
	}

	return fmt.Errorf("operating system %q is not supported. Available prefixes: %s.",
		name, strings.Join(supportedOS[:], ", "))
}

// ValidateArch returns whether a given cpu architecture is supported
func ValidateArch(name string) error {
	for _, arch := range supportedArchitectures {
		if arch == strings.ToLower(name) {
			return nil
		}
	}
	return fmt.Errorf("cpu architecture %q is not supported. Available: %s.",
		name, strings.Join(supportedArchitectures[:], ", "))
}
