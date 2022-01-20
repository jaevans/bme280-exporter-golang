package main

import (
	"fmt"
	"math"
	"net/http"
	"os"

	"github.com/d2r2/go-bsbmp"
	"github.com/d2r2/go-i2c"
	logger "github.com/d2r2/go-logger"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const (
	i2cAddress  = "i2caddress"
	i2cBus      = "i2cbus"
	metricsPort = "port"
	modelName   = "model"
	verbose     = "verbose"
)

var (
	lg logger.PackageLog

	hostname string
	sensor   *bsbmp.BMP
)

type bmeexporter struct {
	Temperature *prometheus.Desc
	Humidity    *prometheus.Desc
	Pressure    *prometheus.Desc
}

// Describe the metrics that we export
func (c *bmeexporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.Temperature
	ch <- c.Humidity
	ch <- c.Pressure
}

// Read the sensor and present the metrics
func (c *bmeexporter) Collect(ch chan<- prometheus.Metric) {
	t, err := sensor.ReadTemperatureC(bsbmp.ACCURACY_HIGH)
	if err != nil {
		lg.Error("Problem reading temp")
	} else {
		ch <- prometheus.MustNewConstMetric(c.Temperature,
			prometheus.GaugeValue,
			math.Round(float64(t)*100)/100,
			hostname,
		)
	}

	// Read atmospheric pressure in pascal
	p, err := sensor.ReadPressurePa(bsbmp.ACCURACY_HIGH)
	if err != nil {
		lg.Error("Problem reading pressure")
	} else {
		ch <- prometheus.MustNewConstMetric(c.Pressure,
			prometheus.GaugeValue,
			math.Round(float64(p)*100)/100,
			hostname,
		)
	}

	// Read atmospheric pressure in mmHg
	supported, h1, err := sensor.ReadHumidityRH(bsbmp.ACCURACY_HIGH)
	if supported {
		if err != nil {
			lg.Error("Problem reading humidity")
		} else {
			ch <- prometheus.MustNewConstMetric(c.Humidity,
				prometheus.GaugeValue,
				math.Round(float64(h1)*100)/100,
				hostname,
			)
		}
	} else {
		lg.Info("Humidity not supported on this sensor")
	}
}

func NewBMEExporter() *bmeexporter {
	sensorName := getSensorName()
	return &bmeexporter{
		Temperature: prometheus.NewDesc("temperature", "Current temperature in celsius", []string{"host"}, prometheus.Labels{"sensor_type": sensorName}),
		Humidity:    prometheus.NewDesc("humidity", "Current realtive humidity", []string{"host"}, prometheus.Labels{"sensor_type": sensorName}),
		Pressure:    prometheus.NewDesc("pressure", "Current atmospheric pressure in hPa", []string{"host"}, prometheus.Labels{"sensor_type": sensorName}),
	}
}

func init() {
	viper.SetDefault(i2cAddress, "0x76")
	viper.SetDefault(i2cBus, 1)
	viper.SetDefault(metricsPort, 8000)
	viper.SetDefault(modelName, "BME280")
	viper.SetDefault(verbose, false)

	// Create the flags with the same names as the viper configuration
	pflag.String(i2cAddress, viper.GetString(i2cAddress), "The I2C address of the sensor")
	pflag.Int(i2cBus, viper.GetInt(i2cBus), "The I2C bus ID")
	pflag.IntP(metricsPort, "p", viper.GetInt(metricsPort), "The port on which to serve metrics")
	pflag.String(modelName, viper.GetString(modelName), "The model of sensor")
	pflag.BoolP(verbose, "v", viper.GetBool(verbose), "Change logging level to verbose")
	pflag.Parse()

	// Bind pflags to viper so they override defaults
	viper.BindPFlags(pflag.CommandLine)

	var err error
	hostname, err = os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	if viper.GetBool(verbose) {
		lg = logger.NewPackageLogger("main", logger.DebugLevel)
	} else {
		lg = logger.NewPackageLogger("main", logger.InfoLevel)
	}
}

func getSensorName() string {
	id, err := sensor.ReadSensorID()
	if err != nil {
		return "unknown"
	}
	switch id {
	case 0x55:
		return "BME180"
	case 0x58:
		return "BMP280"
	case 0x60:
		return "BME280"
	case 0x50:
		return "BME388"
	}
	return "unknown"
}

func getSensorID(name string) (bsbmp.SensorType, error) {
	switch name {
	case "BME180":
		return bsbmp.BMP180, nil
	case "BMP280":
		return bsbmp.BMP280, nil
	case "BME280":
		return bsbmp.BME280, nil
	case "BME388":
		return bsbmp.BMP388, nil
	default:
		return -1, fmt.Errorf("unknown sensor type %s", name)
	}
}

func main() {

	defer logger.FinalizeLogger()

	// Create new connection to i2c-bus on 1 line with address 0x76.
	// Use i2cdetect utility to find device address over the i2c-bus
	i2c, err := i2c.NewI2C(uint8(viper.GetUint(i2cAddress)), viper.GetInt(i2cBus))

	if err != nil {
		lg.Fatal(err)
	}
	defer i2c.Close()

	// Turn down the logging levels for the libraries
	logger.ChangePackageLogLevel("i2c", logger.InfoLevel)
	logger.ChangePackageLogLevel("bsbmp", logger.InfoLevel)

	// Figure out what kind of sensor we have
	modelID, err := getSensorID(viper.GetString(modelName))
	if err != nil {
		lg.Fatal(err)
	}
	sensor, err = bsbmp.NewBMP(modelID, i2c)

	if err != nil {
		lg.Fatal(err)
	}

	id, err := sensor.ReadSensorID()
	if err != nil {
		lg.Fatal(err)
	}
	fmt.Println(id)

	lg.Infof("This Bosch Sensortec sensor has signature: 0x%x", id)

	err = sensor.IsValidCoefficients()
	if err != nil {
		lg.Fatal(err)
	}

	exporter := NewBMEExporter()
	prometheus.MustRegister(exporter)

	// Since all we do is get the info when we're scraped, sit forver serving metrics on the main thread
	serveMetrics()
}

func serveMetrics() {
	http.Handle("/", promhttp.Handler())
	lg.Infof("Listening for metrics on port :%d", viper.GetInt(metricsPort))
	err := http.ListenAndServe(fmt.Sprintf(":%d", viper.GetInt(metricsPort)), nil)
	if err != nil {
		lg.Fatal(err)
	}
}
