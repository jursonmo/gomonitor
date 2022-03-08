package goruntime

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/internal"
	"github.com/influxdata/telegraf/plugins/common/tls"
	"github.com/influxdata/telegraf/plugins/inputs"
)

var DefaulMeasurement = "goruntime_m"

type RuntimeData struct {
	Serial       string           `json:"serial"`
	CPUNum       int              `json:"cpuNum"`
	ThreadNum    int              `json:"threadNum"`
	GoRoutineNum int              `json:"goroutineNum"`
	CpuPercent   int              `json:"cpuPercent"`
	MemPercent   int              `json:"memPercent"`
	Memstats     runtime.MemStats `json:"memstats"`
}

type GoRuntime struct {
	Urls        []string `toml:"urls"`
	Method      string   `toml:"method"`
	Measurement string   `toml:"measurement"`

	// HTTP Basic Auth Credentials
	Username string `toml:"username"`
	Password string `toml:"password"`
	tls.ClientConfig

	Timeout internal.Duration `toml:"timeout"`

	client *http.Client
}

var sampleConfig = `
# Read formatted metrics from one or more xxx endpoints
[[inputs.goruntime]]
  ## One or more URLs from which to read formatted metrics
  urls = ["http://localhost:8062/debug/vars"]

  ## HTTP method
  # method = "GET"

  measurement = "goruntime_mea"

  ## Optional HTTP Basic Auth Credentials
  # username = "username"
  # password = "pa$$word"

  ## Optional TLS Config
  # tls_ca = "/etc/telegraf/ca.pem"
  # tls_cert = "/etc/telegraf/cert.pem"
  # tls_key = "/etc/telegraf/key.pem"
  ## Use TLS but skip chain & host verification
  # insecure_skip_verify = false

  ## Amount of time allowed to complete the HTTP request
  # timeout = "5s"
`

func init() {
	inputs.Add("goruntime", func() telegraf.Input {
		return &GoRuntime{
			Timeout: internal.Duration{Duration: time.Second * 5},
			Method:  "GET",
		}
	})
}

// SampleConfig returns the default configuration of the Input
func (*GoRuntime) SampleConfig() string {
	return sampleConfig
}

// Description returns a one-sentence description on the Input
func (*GoRuntime) Description() string {
	return "Read formatted metrics from GoRuntime"
}

// Gather takes in an accumulator and adds the metrics that the Input
// gathers. This is called every "interval"
func (c *GoRuntime) Gather(acc telegraf.Accumulator) error {
	if c.client == nil {
		tlsCfg, err := c.ClientConfig.TLSConfig()
		if err != nil {
			return err
		}
		c.client = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsCfg,
				Proxy:           http.ProxyFromEnvironment,
			},
			Timeout: c.Timeout.Duration,
		}
	}

	var wg sync.WaitGroup
	for _, u := range c.Urls {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			if err := c.gatherURL(acc, url); err != nil {
				acc.AddError(fmt.Errorf("[url=%s]: %s", url, err))
			}
		}(u)
	}

	wg.Wait()

	return nil
}

// Gathers data from a particular URL
// Parameters:
//     acc    : The telegraf Accumulator to use
//     url    : endpoint to send request to
//
// Returns:
//     error: Any error that may have occurred
func (c *GoRuntime) gatherURL(acc telegraf.Accumulator, url string) error {
	request, err := http.NewRequest(c.Method, url, nil)
	if err != nil {
		return err
	}

	if c.Username != "" || c.Password != "" {
		request.SetBasicAuth(c.Username, c.Password)
	}

	resp, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Received status code %d (%s), expected %d (%s)",
			resp.StatusCode,
			http.StatusText(resp.StatusCode),
			http.StatusOK,
			http.StatusText(http.StatusOK))
	}
	decoder := json.NewDecoder(resp.Body)

	var data RuntimeData
	err = decoder.Decode(&data)
	if err != nil {
		return err
	}
	return c.parse(&data, acc)
}

func (c *GoRuntime) parse(rd *RuntimeData, acc telegraf.Accumulator) error {
	fields := Fields{}
	fields.Serial = rd.Serial
	fields.NumCpu = int64(rd.CPUNum)
	fields.NumGoroutine = int64(rd.GoRoutineNum)
	fields.NumThread = int64(rd.ThreadNum)
	fields.CpuPercent = int64(rd.CpuPercent)
	fields.MemPercent = int64(rd.MemPercent)

	collectMemStats(&fields, &rd.Memstats)
	collectGCStats(&fields, &rd.Memstats)

	measurement := c.Measurement
	if measurement == "" {
		measurement = DefaulMeasurement
	}
	acc.AddGauge(measurement, fields.Values(), fields.Tags())
	return nil
}
