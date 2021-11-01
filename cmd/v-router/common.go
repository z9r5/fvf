package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type GlobalConfigType struct {
	ListenAddress    string `default:"0.0.0.0"`
	ListenPort       string `default:"8080"`
	LogLevel         string `default:"warn"`
	LogFormat        string `default:"text"`
	PathChannelsFile string `default:"channels.yaml"`
	PathStatic       string `default:"root"`
	PathTpls         string `default:"/includes"`
	LocationVersions string `default:"/documentation"`
	I18nType         string `default:"domain"`
	URLValidation    bool   `default:"false"`
}

type ChannelType struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ReleaseType struct {
	Name     string
	Channels []ChannelType
}

type ReleasesStatusType struct {
	Groups []ReleaseType
}

type APIStatusResponseType struct {
	Status         string        `json:"status"`
	Msg            string        `json:"msg"`
	RootVersion    string        `json:"rootVersion"`
	RootVersionURL string        `json:"rootVersionURL"`
	Releases       []ReleaseType `json:"releasechannels"`
}

type templateDataType struct {
	VersionItems           []versionMenuItems
	HTMLContent            string
	CurrentGroup           string
	CurrentChannel         string
	CurrentVersion         string
	CurrentLang            string
	AbsoluteVersion        string // Contains explicit version, used for getting git link to source file
	CurrentVersionURL      string
	CurrentPageURLRelative string // Relative URL, without "<lang>/<LocationVersions>/<version>"
	CurrentPageURL         string // Full page URL
	MenuDocumentationLink  string // E.g. Used for top menus
}

type versionMenuItems struct {
	Group      string
	Channel    string
	Version    string
	VersionURL string // Base URL for corresponding version without a leading /, e.g. 'v1.2.3-plus-fix6'.
	IsCurrent  bool
}

var ReleasesStatus ReleasesStatusType

var channelsListReverseStability = []string{"rock-solid", "stable", "ea", "beta", "alpha"}

func ValidateConfig() {
	if GlobalConfig.I18nType != "domain" && GlobalConfig.I18nType != "location" {
		log.Fatalln(fmt.Sprintf("Unknown localization method specified (%s). It can be 'domain' or 'location'.", GlobalConfig.I18nType))
	}
	// Check template directory
	if fi, err := os.Stat(getRootFilesPath() + GlobalConfig.PathTpls); err == nil {
		if !fi.IsDir() {
			log.Fatalln(fmt.Sprintf("The '%s%s' directory, specified as the templates directory — is not a directory", getRootFilesPath(), GlobalConfig.PathTpls))
		}
	} else {
		log.Fatalln(fmt.Sprintf("Template directory '%s' doesn't exist", GlobalConfig.PathTpls))
	}
	// Check channels file
	if _, err := os.Stat(GlobalConfig.PathChannelsFile); err != nil {
		if os.IsNotExist(err) {
			log.Fatalln(fmt.Sprintf("Channels file '%s' doesn't exist", GlobalConfig.PathChannelsFile))
		}
		log.Fatalln(fmt.Sprintf("Channels file '%s' access error", GlobalConfig.PathChannelsFile))
	}
}

func printConfiguration() {
	log.Infoln(fmt.Sprintf("Listening on %s:%s", GlobalConfig.ListenAddress, GlobalConfig.ListenPort))
	log.Infoln(fmt.Sprintf("Logging level is %s (format - %s)", log.GetLevel(), GlobalConfig.LogFormat))
	dir, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	log.Infoln(fmt.Sprintf("Working dir: %s", dir))
	log.Infoln(fmt.Sprintf("Channel file used: %s", GlobalConfig.PathChannelsFile))
	log.Infoln(fmt.Sprintf("Directory with static files: %s", getRootFilesPath()))
	log.Infoln(fmt.Sprintf("Templates directory: %s%s", getRootFilesPath(), GlobalConfig.PathTpls))
	log.Infoln(fmt.Sprintf("URL location for versions: %s", GlobalConfig.LocationVersions))
	log.Infoln(fmt.Sprintf("Localization method: %s", GlobalConfig.I18nType))

	if log.GetLevel() == log.TraceLevel {
		channelFileContent, err := ioutil.ReadFile(GlobalConfig.PathChannelsFile)

		if err != nil {
			log.Fatal(err)
		}

		fmt.Println(string(channelFileContent))
	}
}

func getRootRelease() (activeRelease string) {
	if len(os.Getenv("ACTIVE_RELEASE")) > 0 {
		activeRelease = os.Getenv("ACTIVE_RELEASE")
	} else {
		activeRelease = "v1"
	}
	return
}

func (m *templateDataType) getChannelMenuData(r *http.Request, releases *ReleasesStatusType) (err error) {
	err = nil

	m.CurrentPageURLRelative = getDocPageURLRelative(r, false)
	m.CurrentPageURL = getCurrentPageURL(r)
	m.CurrentVersionURL = getVersionURL(r)
	m.CurrentLang = getCurrentLang(r)

	if isGroupChannelURL, _ := regexp.MatchString("v[0-9]+.[0-9]+-(alpha|beta|ea|stable|rock-solid)", m.CurrentVersionURL); isGroupChannelURL {
		items := strings.Split(m.CurrentVersionURL, "-")
		if len(items) == 2 {
			m.CurrentGroup = items[0]
			m.CurrentChannel = items[1]
			m.CurrentVersion, _ = getVersionFromChannelAndGroup(releases, m.CurrentChannel, m.CurrentGroup)
			m.CurrentVersionURL = VersionToURL(m.CurrentVersion)
		}
	} else {
		m.CurrentVersion = URLToVersion(m.CurrentVersionURL)
	}

	m.CurrentVersion = URLToVersion(m.CurrentVersionURL)

	if m.CurrentVersion == "" {
		m.CurrentVersion = getRootRelease()
		m.CurrentVersionURL = VersionToURL(m.CurrentVersion)
	}

	// Try to find current channel from URL
	if m.CurrentChannel == "" || m.CurrentGroup == "" {
		m.CurrentChannel, m.CurrentGroup = getChannelAndGroupFromVersion(releases, m.CurrentVersion)
	}

	// Add the first menu item
	m.VersionItems = append(m.VersionItems, versionMenuItems{
		Group:      m.CurrentGroup,
		Channel:    m.CurrentChannel,
		Version:    m.CurrentVersion,
		VersionURL: m.CurrentVersionURL,
		IsCurrent:  true,
	})

	// Add other items
	for _, group := range getGroups() {
		// TODO error handling
		_ = m.getChannelsFromGroup(&ReleasesStatus, group)
	}

	return
}

func (m *templateDataType) getVersionMenuData(r *http.Request) (err error) {
	err = nil

	m.CurrentPageURLRelative = getDocPageURLRelative(r, false)
	m.CurrentPageURL = getCurrentPageURL(r)
	m.CurrentVersionURL = getVersionURL(r)
	m.CurrentVersion = URLToVersion(m.CurrentVersionURL)
	m.CurrentLang = getCurrentLang(r)

	if m.CurrentVersion == "" {
		re := regexp.MustCompile(fmt.Sprintf("^/[^/]%s/(.+)$", GlobalConfig.LocationVersions))
		res := re.FindStringSubmatch(m.CurrentPageURL)
		if res == nil {
			m.MenuDocumentationLink = ""
		} else {
			m.CurrentVersion = getRootRelease()
			m.CurrentVersionURL = VersionToURL(m.CurrentVersion)
		}
	}

	re := regexp.MustCompile(`^(v[0-9]+)(\..+)?$`)
	res := re.FindStringSubmatch(m.CurrentVersion)
	if res != nil {
		if res[2] != "" {
			// Version is not a group (MAJ.MIN), but the patch version
			m.MenuDocumentationLink = fmt.Sprintf("%s/%s/", GlobalConfig.LocationVersions, VersionToURL(res[1]))
			m.AbsoluteVersion = m.CurrentVersion
		} else {
			m.MenuDocumentationLink = fmt.Sprintf("%s/%s/", GlobalConfig.LocationVersions, VersionToURL(m.CurrentVersion))
			m.AbsoluteVersion, err = getVersionFromGroup(&ReleasesStatus, res[1])
			if err != nil {
				log.Debugln(fmt.Sprintf("getVersionMenuData: error determine absolute version for %s (got %s)", m.CurrentVersion, m.AbsoluteVersion))
			}
		}
	}

	// Add the first menu item
	m.VersionItems = append(m.VersionItems, versionMenuItems{
		Group:      m.CurrentGroup,
		Channel:    m.CurrentChannel,
		Version:    m.CurrentVersion,
		VersionURL: m.CurrentVersionURL,
		IsCurrent:  true,
	})

	// Add other items
	for _, group := range getGroups() {
		// TODO error handling
		_ = m.getChannelsFromGroup(&ReleasesStatus, group)
	}

	return
}

func (m *templateDataType) getGroupMenuData(r *http.Request) (err error) {
	err = nil

	m.CurrentPageURLRelative = getDocPageURLRelative(r, false)
	m.CurrentPageURL = getCurrentPageURL(r)
	m.CurrentVersionURL = getVersionURL(r)
	m.CurrentVersion = URLToVersion(m.CurrentVersionURL)
	m.CurrentLang = getCurrentLang(r)

	if m.CurrentVersion == "" {
		m.CurrentVersion = getRootRelease()
		m.CurrentVersionURL = VersionToURL(m.CurrentVersion)
	}

	re := regexp.MustCompile(`^(v[0-9]+)$`)
	res := re.FindStringSubmatch(m.CurrentVersion)
	if res != nil {
		m.VersionItems = append(m.VersionItems, versionMenuItems{
			Group:      res[1],
			Channel:    "",
			Version:    m.CurrentVersion,
			VersionURL: m.CurrentVersionURL,
			IsCurrent:  true,
		})
	} else {
		// Version is not a group (MAJ.MIN), but the patch version
		m.VersionItems = append(m.VersionItems, versionMenuItems{
			Group:      "",
			Channel:    "",
			Version:    m.CurrentVersion,
			VersionURL: m.CurrentVersionURL,
			IsCurrent:  true,
		})
	}

	// Add other items
	for _, group := range getGroups() {
		// TODO error handling
		if group == "1.0" {
			continue
		}
		m.VersionItems = append(m.VersionItems, versionMenuItems{
			Group:      group,
			Channel:    "",
			Version:    "",
			VersionURL: "",
			IsCurrent:  false,
		})
	}

	return
}

// Get channels and corresponding versions for the specified
// group according to the reverse order of stability
func (m *templateDataType) getChannelsFromGroup(releases *ReleasesStatusType, group string) (err error) {
	for _, item := range releases.Groups {
		if item.Name == group {
			for _, channel := range channelsListReverseStability {
				for _, channelItem := range item.Channels {
					if channelItem.Name == channel {
						m.VersionItems = append(m.VersionItems, versionMenuItems{
							Group:      group,
							Channel:    channelItem.Name,
							Version:    channelItem.Version,
							VersionURL: VersionToURL(channelItem.Version),
							IsCurrent:  false,
						})
					}
				}
			}
		}
	}
	return
}

// Get channel and group for specified version
func getChannelAndGroupFromVersion(releases *ReleasesStatusType, version string) (channel, group string) {

	re := regexp.MustCompile(`^(v[0-9]+)$`)
	res := re.FindStringSubmatch(version)
	if res != nil {
		return "", res[1]
	}

	for _, group := range getGroups() {
		for _, channel := range channelsListReverseStability {
			for _, releaseItem := range releases.Groups {
				if releaseItem.Name == group {
					for _, channelItem := range releaseItem.Channels {
						if channelItem.Name == channel {
							if channelItem.Version == version {
								return channel, group
							}
						}
					}
				}
			}
		}
	}
	return
}

// Get version for specified group and channel
func getVersionFromChannelAndGroup(releases *ReleasesStatusType, channel, group string) (version string, err error) {
	for _, releaseItem := range releases.Groups {
		if releaseItem.Name == group {
			for _, channelItem := range releaseItem.Channels {
				if channelItem.Name == channel {
					return channelItem.Version, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no matching version for group %s, channel %s", group, channel)
}

// Gev version from specified group
// E.g. get v1.2.3+fix6 from v1.2
func getVersionFromGroup(releases *ReleasesStatusType, group string) (version string, err error) {
	if len(releases.Groups) > 0 {
		for _, ReleaseGroup := range releases.Groups {
			if ReleaseGroup.Name == group {
				releaseVersions := make(map[string]string)
				for _, channel := range ReleaseGroup.Channels {
					releaseVersions[channel.Name] = channel.Version
				}

				if _, ok := releaseVersions["stable"]; ok {
					return releaseVersions["stable"], nil
				} else if _, ok := releaseVersions["ea"]; ok {
					return releaseVersions["ea"], nil
				} else if _, ok := releaseVersions["beta"]; ok {
					return releaseVersions["beta"], nil
				} else if _, ok := releaseVersions["alpha"]; ok {
					return releaseVersions["alpha"], nil
				}
			}
		}
	}

	return "", fmt.Errorf("can't get version for %s", group)

}

func getRootReleaseVersion() string {
	activeRelease := getRootRelease()

	_ = updateReleasesStatus()

	if len(ReleasesStatus.Groups) > 0 {
		for _, ReleaseGroup := range ReleasesStatus.Groups {
			if ReleaseGroup.Name == activeRelease {
				releaseVersions := make(map[string]string)
				for _, channel := range ReleaseGroup.Channels {
					releaseVersions[channel.Name] = channel.Version
				}

				if _, ok := releaseVersions["stable"]; ok {
					return releaseVersions["stable"]
				} else if _, ok := releaseVersions["ea"]; ok {
					return releaseVersions["ea"]
				} else if _, ok := releaseVersions["beta"]; ok {
					return releaseVersions["beta"]
				} else if _, ok := releaseVersions["alpha"]; ok {
					return releaseVersions["alpha"]
				}
			}
		}
	}
	return "unknown"
}

// Get the full page URL menu requested for
// E.g /documentation/v1.2.3/reference/build_process.html
func getCurrentPageURL(r *http.Request) (result string) {

	originalURI, err := url.Parse(r.Header.Get("x-original-uri"))
	if err != nil {
		return
	}

	if originalURI.Path == "/404.html" {
		return
	}
	return originalURI.Path
}

// Get the full page URL menu requested for
// E.g /documentation/v1.2.3/reference/build_process.html
func getCurrentLang(r *http.Request) (result string) {
	result = "en"
	originalURI, err := url.Parse(r.Header.Get("x-original-uri"))
	if err != nil {
		return
	}

	if originalURI.Path == "/404.html" {
		return
	}

	re := regexp.MustCompile(fmt.Sprintf("^/(ru|en)%s/.+$", GlobalConfig.LocationVersions))
	res := re.FindStringSubmatch(originalURI.Path)
	if res != nil {
		result = res[1]
	}
	return

}

// Get page URL menu requested for without a leading version suffix
// E.g /reference/build_process.html for /documentation/v1.2.3/reference/build_process.html
// if useURI == true - use requestURI instead of x-original-uri header value
func getDocPageURLRelative(r *http.Request, useURI bool) (result string) {
	var (
		URLtoParse  string
		originalURI *url.URL
		err         error
	)

	if useURI {
		originalURI, err = url.Parse(r.RequestURI)
	} else {
		originalURI, err = url.Parse(r.Header.Get("x-original-uri"))
	}

	if err != nil {
		return
	}

	if originalURI.Path == "/404.html" {
		return
	}
	URLtoParse = originalURI.Path

	re := regexp.MustCompile(fmt.Sprintf("^/(ru|en)(%s/[^/]+)?/(.*)$", GlobalConfig.LocationVersions))
	res := re.FindStringSubmatch(URLtoParse)
	if res != nil {
		if len(res[2]) > 0 {
			result = res[3]
		} else {
			result = fmt.Sprintf("%s/%s", res[2], res[3])
		}
	}
	return
}

// Get version URL page belongs to if request came from concrete documentation version, otherwise empty.
// E.g for the /documentation/v1.2.3-plus-fix5/reference/build_process.html return "v1.2.3-plus-fix5".
func getVersionURL(r *http.Request) (result string) {
	URLtoParse := ""
	originalURI, err := url.Parse(r.Header.Get("x-original-uri"))

	if err != nil {
		return
	}

	if originalURI.Path == "/404.html" {
		values, err := url.ParseQuery(originalURI.RawQuery)
		if err != nil {
			return
		}
		URLtoParse = values.Get("uri")
	} else {
		URLtoParse = originalURI.Path
	}

	re := regexp.MustCompile(fmt.Sprintf("^/(ru|en)%s/([^/]+)/?.*$", GlobalConfig.LocationVersions))
	res := re.FindStringSubmatch(URLtoParse)
	if res != nil {
		result = res[2]
	}

	return strings.TrimPrefix(result, "/")
}

func VersionToURL(version string) string {
	result := strings.ReplaceAll(version, "+", "-plus-")
	result = strings.ReplaceAll(result, "_", "-u-")
	return result
}

func URLToVersion(version string) (result string) {
	result = strings.ReplaceAll(version, "-plus-", "+")
	result = strings.ReplaceAll(result, "-u-", "_")
	return
}

func validateURL(url string) (err error) {
	if ! GlobalConfig.URLValidation {
		return nil
	}

	var resp *http.Response
	allowedStatusCodes := []int{200, 401}
	tries := 3
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 10 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       10 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

	for {
		resp, err = client.Get(url)
		log.Tracef("Validating %s (tries-%v):\nStatus - %v\nHeader - %+v,", url, tries, resp.Status, resp.Header)
		if err == nil && (resp.StatusCode == 301 || resp.StatusCode == 302) {
			if len(resp.Header.Get("Location")) > 0 {
				url = resp.Header.Get("Location")
			} else {
				tries = 0
			}
			tries--
		} else {
			tries = 0
		}
		if tries < 1 {
			break
		}
	}

	if err == nil {
		place := sort.SearchInts(allowedStatusCodes, resp.StatusCode)
		if place >= len(allowedStatusCodes) {
			err = fmt.Errorf("%s is not valid", url)
		}
	}
	return
}

// Get update channel groups in a descending order.
func getGroups() (groups []string) {
	for _, item := range ReleasesStatus.Groups {
		groups = append(groups, item.Name)
	}
	// TODO compare groups function
	sort.Slice(groups, func(i, j int) bool {
		var _i, _j float64
		var err error
		if _i, err = strconv.ParseFloat(groups[i], 32); err != nil {
			_i = 0
		}
		if _j, err = strconv.ParseFloat(groups[j], 32); err != nil {
			_j = 0
		}
		return _i > _j
	})
	return
}

func getRootFilesPath() string {
	return GlobalConfig.PathStatic
}

func unmarshalJSON(data []byte, config interface{}) error {
	err := json.Unmarshal(data, config)
	if err != nil {
		log.Errorf("Can't unmarshall %s (%e)", GlobalConfig.PathChannelsFile, err)
		return err
	}
	return nil
}

func unmarshalYAML(data []byte, config interface{}) error {
	err := yaml.Unmarshal(data, config)
	if err != nil {
		log.Errorf("Can't unmarshall %s (%e)", GlobalConfig.PathChannelsFile, err)
		return err
	}
	return nil
}

func updateReleasesStatus() error {
	data, err := ioutil.ReadFile(GlobalConfig.PathChannelsFile)
	if err != nil {
		log.Errorf("Can't open %s (%e)", GlobalConfig.PathChannelsFile, err)
		return err
	}
	if strings.HasSuffix(GlobalConfig.PathChannelsFile, ".json") {
		return unmarshalJSON(data, &ReleasesStatus)
	} else if strings.HasSuffix(GlobalConfig.PathChannelsFile, ".yaml") || strings.HasSuffix(GlobalConfig.PathChannelsFile, ".yml") {
		return unmarshalYAML(data, &ReleasesStatus)
	}
	return fmt.Errorf("failed to decode channels file %s", GlobalConfig.PathChannelsFile)
}
