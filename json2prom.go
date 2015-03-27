package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

type collector struct {
	sources []*source
}

type source struct {
	URL       string
	Namespace string
	Subsystem string
	Labels    map[string]string
	Keys      map[string]struct {
		Skip           bool
		MapValue       map[string]float64
		UseKeysAsLabel string
	}
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
	http.Handle("/metrics", prometheus.Handler())

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
	for _, s := range c.sources {
		resp, err := http.Get(s.URL)
		if err != nil {
			log.Print(err)
			continue
		}
		defer resp.Body.Close()

		var value interface{}
		if err := json.NewDecoder(resp.Body).Decode(&value); err != nil {
			log.Print(err)
			continue
		}

		var labelNames []string
		var labelValues []string
		for k, v := range s.Labels {
			labelNames = append(labelNames, k)
			labelValues = append(labelValues, v)
		}
		s.processValue(nil, labelNames, labelValues, value, metrics)
	}
}

func (s *source) processValue(keys []string, labelNames, labelValues []string, value interface{}, metrics chan<- prometheus.Metric) {
	switch value := value.(type) {
	case map[string]interface{}:
		for k2, v2 := range value {
			d := s.Keys[k2]
			if d.Skip {
				continue
			}
			if d.MapValue != nil {
				s.processValue(append(keys, strings.Trim(k2, "_")), labelNames, labelValues, d.MapValue[v2.(string)], metrics)
				continue
			}
			if d.UseKeysAsLabel != "" {
				for k3, v3 := range v2.(map[string]interface{}) {
					s.processValue(append(keys, strings.Trim(k2, "_")), append(labelNames, d.UseKeysAsLabel), append(labelValues, k3), v3, metrics)
				}
				continue
			}
			s.processValue(append(keys, strings.Trim(k2, "_")), labelNames, labelValues, v2, metrics)
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
