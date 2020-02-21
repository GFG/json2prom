package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

var client = &http.Client{
	Timeout: 5 * time.Second,
}

type collector struct {
	sources []*source
}

type source struct {
	URL       string
	Namespace string
	Subsystem string
	Labels    map[string]string
	ErrorKey  string
	Keys      map[string]action
}

type action struct {
	Skip      bool
	MapValue  map[string]float64
	MakeLabel string
	LabelKey  string
}

func main() {
	sourcesJSON := os.Getenv("SOURCES")
	if sourcesJSON == "" {
		log.Print("environemnt variable SOURCES is requred")
		return
	}

	var sources []*source
	if err := json.Unmarshal([]byte(sourcesJSON), &sources); err != nil {
		log.Print(err)
		return
	}

	prometheus.MustRegister(&collector{sources})
	http.Handle("/", http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		resp.Write([]byte(`<html><head><title>json2prom</title></head><body><h1>json2prom</h1><p><a href="/metrics">Metrics</a></p></body></html>`))
	}))
	http.Handle("/metrics", promhttp.Handler())

	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	fmt.Println("listening on " + addr)
	log.Print(http.ListenAndServe(addr, nil))
}

func (c *collector) Describe(descs chan<- *prometheus.Desc) {
	metrics := make(chan prometheus.Metric)
	done := make(chan struct{})
	go func() {
		for m := range metrics {
			descs <- m.Desc()
		}
		close(done)
	}()
	c.Collect(metrics)
	close(metrics)
	<-done
}

func (c *collector) Collect(metrics chan<- prometheus.Metric) {
	var wg sync.WaitGroup
	for _, s := range c.sources {
		wg.Add(1)
		go func(s *source) {
			defer wg.Done()

			resp, err := client.Get(s.URL)
			if err != nil {
				log.Print(err)
				return
			}
			defer resp.Body.Close()

			var value interface{}
			if err := json.NewDecoder(resp.Body).Decode(&value); err != nil {
				log.Print(err)
				return
			}

			if s.ErrorKey != "" {
				if err, ok := value.(map[string]interface{})[s.ErrorKey]; ok {
					log.Print(err)
					return
				}
			}

			var labelNames []string
			var labelValues []string
			for k, v := range s.Labels {
				labelNames = append(labelNames, k)
				labelValues = append(labelValues, v)
			}
			s.processValue(nil, labelNames, labelValues, value, s.Keys["^"], metrics)
		}(s)
	}
	wg.Wait()
}

func (s *source) processValue(keys []string, labelNames, labelValues []string, value interface{}, act action, metrics chan<- prometheus.Metric) {
	if act.Skip {
		return
	}
	if act.MapValue != nil {
		value = act.MapValue[value.(string)]
	}
	if act.MakeLabel != "" {
		for k, v := range value.(map[string]interface{}) {
			labelValue := k
			if act.LabelKey != "" {
				labelValue = v.(map[string]interface{})[act.LabelKey].(string)
			}
			s.processValue(keys, append(labelNames, act.MakeLabel), append(labelValues, labelValue), v, action{}, metrics)
		}
		return
	}

	switch value := value.(type) {
	case map[string]interface{}:
		for k, v := range value {
			s.processValue(append(keys, strings.Trim(k, "_")), labelNames, labelValues, v, s.Keys[k], metrics)
		}
	case float64:
		if value == 0 {
			return
		}
		labels := make(prometheus.Labels)
		for i, name := range labelNames {
			labels[name] = labelValues[i]
		}
		g := prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace:   s.Namespace,
			Subsystem:   s.Subsystem,
			Name:        strings.Join(keys, "_"),
			Help:        strings.Join(keys, "."),
			ConstLabels: labels,
		})
		g.Set(value)
		metrics <- g
	}
}
