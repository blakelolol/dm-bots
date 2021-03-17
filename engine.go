package igopher

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/hbollon/igopher/internal/proxy"
	"github.com/hbollon/selenium"
	"github.com/hbollon/selenium/chrome"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
)

const (
	locatorID    = "ID"
	locatorName  = "NAME"
	locatorXPath = "XPATH"
	locatorCSS   = "CSS"
)

var (
	seleniumPath                                  = filepath.FromSlash("./lib/selenium-server.jar")
	chromePath, chromeDriverPath, geckoDriverPath string
)

func init() {
	if runtime.GOOS == "windows" {
		geckoDriverPath = filepath.FromSlash("./lib/geckodriver.exe")
		chromeDriverPath = filepath.FromSlash("./lib/chromedriver.exe")
		chromePath = filepath.FromSlash("./lib/chrome-win/chrome.exe")
	} else if runtime.GOOS == "darwin" {
		geckoDriverPath = filepath.FromSlash("./lib/geckodriver")
		chromeDriverPath = filepath.FromSlash("./lib/chromedriver")
		chromePath = filepath.FromSlash("./lib/chrome-mac/Chromium.app/Contents/MacOS/Chromium")
	} else {
		geckoDriverPath = filepath.FromSlash("./lib/geckodriver")
		chromeDriverPath = filepath.FromSlash("./lib/chromedriver")
		chromePath = filepath.FromSlash("./lib/chrome-linux/chrome")
	}
}

// Selenium instance and opts
type Selenium struct {
	Instance           *selenium.Service
	Config             *ClientConfig
	Opts               []selenium.ServiceOption
	Proxy              proxy.Proxy `yaml:"proxy"`
	WebDriver          selenium.WebDriver
	SigTermRoutineExit chan bool
}

// InitializeSelenium start a Selenium WebDriver server instance
// (if one is not already running).
func (s *Selenium) InitializeSelenium(clientConfig *ClientConfig) {
	var err error
	s.Config = clientConfig

	var output *os.File
	if s.Config.Debug {
		output = os.Stderr
	} else {
		output = nil
	}

	s.Opts = []selenium.ServiceOption{
		selenium.GeckoDriver(geckoDriverPath),   // Specify the path to GeckoDriver in order to use Firefox.
		selenium.ChromeDriver(chromeDriverPath), // Specify the path to ChromeDriver in order to use Chrome.
		selenium.Output(output),                 // Output debug information to stderr.
	}
	if s.Config.Headless {
		s.Opts = append(s.Opts, selenium.StartFrameBuffer())
	}

	selenium.SetDebug(s.Config.Debug)
	s.Instance, err = selenium.NewSeleniumService(seleniumPath, int(s.Config.Port), s.Opts...)
	if err != nil {
		log.Fatal(err) // Fatal error, exit if webdriver can't be initialize.
	}

	if s.SigTermRoutineExit == nil {
		s.SigTermCleaning()
	}
}

// InitFirefoxWebDriver init and launch web driver with Firefox
func (s *Selenium) InitFirefoxWebDriver() {
	var err error
	caps := selenium.Capabilities{"browserName": "firefox"}
	s.WebDriver, err = selenium.NewRemote(caps, fmt.Sprintf("http://localhost:%d/wd/hub", s.Config.Port))
	if err != nil {
		log.Error(err)
	}
}

// InitChromeWebDriver init and launch web driver with Chrome
func (s *Selenium) InitChromeWebDriver() {
	var err error
	caps := selenium.Capabilities{"browserName": "chrome"}
	chromeCaps := chrome.Capabilities{
		Path: filepath.FromSlash(chromePath),
		Args: []string{
			"--disable-extensions",
			"--disable-infobars",
			"--no-sandbox",
			"--window-size=360,640",
		},
		ExcludeSwitches: []string{"enable-automation"},
		MobileEmulation: &chrome.MobileEmulation{
			DeviceName: "Nexus 5",
		},
	}
	caps.AddChrome(chromeCaps)
	if s.Proxy.Enabled {
		logrus.Debug("Proxy activated.")
		if err = proxy.InitForwardingProxy(8880, s.Proxy); err == nil {
			caps.AddProxy(selenium.Proxy{
				Type:    selenium.Manual,
				HTTP:    fmt.Sprintf("localhost:%d", 8880),
				FTP:     fmt.Sprintf("localhost:%d", 8880),
				SSL:     fmt.Sprintf("localhost:%d", 8880),
				NoProxy: nil,
			})
		} else {
			log.Errorf("Failed to initialize proxy forwarder: %v", err)
		}
		// caps.AddProxy(selenium.Proxy{
		// 	Type:    selenium.Manual,
		// 	HTTP:    fmt.Sprintf("localhost:%d", 8880),
		// 	FTP:     fmt.Sprintf("localhost:%d", 8880),
		// 	SSL:     fmt.Sprintf("localhost:%d", 8880),
		// 	NoProxy: nil,
		// })
	}

	s.WebDriver, err = selenium.NewRemote(caps, fmt.Sprintf("http://localhost:%d/wd/hub", s.Config.Port))
	if err != nil {
		log.Error(err)
	}
}

// CloseSelenium close webdriver and selenium instances
func (s *Selenium) CloseSelenium() {
	if s.WebDriver != nil {
		s.WebDriver.Close()
		s.WebDriver.Quit()
		s.WebDriver = nil
		logrus.Debug("Closed webdriver")
	}
	if s.Instance != nil {
		s.Instance.Stop()
		s.Instance = nil
		logrus.Debug("Closed selenium instance")
	}
}

// SigTermCleaning launch a gouroutine to handle SigTerm signal and trigger Selenium and Webdriver closing if it raised
func (s *Selenium) SigTermCleaning() {
	sig := make(chan os.Signal)
	s.SigTermRoutineExit = make(chan bool)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		for {
			select {
			case <-sig:
				s.CloseSelenium()
				os.Exit(1)
			case <-s.SigTermRoutineExit:
				s.SigTermRoutineExit = nil
				return
			default:
				break
			}
		}
	}()
}

/* Browser methods */

// IsElementPresent check if an element is present on the current webpage
func (s *Selenium) IsElementPresent(by, value string) bool {
	_, err := s.WebDriver.FindElement(by, value)
	if err != nil {
		log.Debugf("Element not found by %s: %v", by, err)
		return false
	}
	return true
}

// GetElement wait for element and then return when it's available
func (s *Selenium) GetElement(elementTag, locator string) (selenium.WebElement, error) {
	locator = strings.ToUpper(locator)
	if locator == locatorID && s.IsElementPresent(selenium.ByID, elementTag) {
		return s.WebDriver.FindElement(selenium.ByID, elementTag)
	} else if locator == locatorName && s.IsElementPresent(selenium.ByName, elementTag) {
		return s.WebDriver.FindElement(selenium.ByName, elementTag)
	} else if locator == locatorXPath && s.IsElementPresent(selenium.ByXPATH, elementTag) {
		return s.WebDriver.FindElement(selenium.ByXPATH, elementTag)
	} else if locator == locatorCSS && s.IsElementPresent(selenium.ByCSSSelector, elementTag) {
		return s.WebDriver.FindElement(selenium.ByCSSSelector, elementTag)
	} else {
		log.Debugf("Incorrect locator '%s'", locator)
		return nil, errors.New("Incorrect locator")
	}
}

// GetElements wait for elements and then return when they're available
func (s *Selenium) GetElements(elementTag, locator string) ([]selenium.WebElement, error) {
	locator = strings.ToUpper(locator)
	if locator == locatorID && s.IsElementPresent(selenium.ByID, elementTag) {
		return s.WebDriver.FindElements(selenium.ByID, elementTag)
	} else if locator == locatorName && s.IsElementPresent(selenium.ByName, elementTag) {
		return s.WebDriver.FindElements(selenium.ByName, elementTag)
	} else if locator == locatorXPath && s.IsElementPresent(selenium.ByXPATH, elementTag) {
		return s.WebDriver.FindElements(selenium.ByXPATH, elementTag)
	} else if locator == locatorCSS && s.IsElementPresent(selenium.ByCSSSelector, elementTag) {
		return s.WebDriver.FindElements(selenium.ByCSSSelector, elementTag)
	} else {
		log.Debugf("Incorrect locator '%s'", locator)
		return nil, errors.New("Incorrect locator")
	}
}

// WaitForElement search and wait until searched element appears.
// Delay argument is in seconds.
func (s *Selenium) WaitForElement(elementTag, locator string, delay int) (bool, error) {
	locator = strings.ToUpper(locator)
	s.WebDriver.SetImplicitWaitTimeout(0)
	defer s.WebDriver.SetImplicitWaitTimeout(30)

	timeout := time.After(time.Duration(delay) * time.Second)
	tick := time.NewTicker(500 * time.Millisecond)
	for {
		select {
		case <-timeout:
			return false, errors.New("Timed out : element not found")
		case <-tick.C:
			if (locator == locatorID && s.IsElementPresent(selenium.ByID, elementTag)) ||
				(locator == locatorName && s.IsElementPresent(selenium.ByName, elementTag)) ||
				(locator == locatorXPath && s.IsElementPresent(selenium.ByXPATH, elementTag)) ||
				(locator == locatorCSS && s.IsElementPresent(selenium.ByCSSSelector, elementTag)) {
				return true, nil
			}
		}
	}
}
