package main

import (
	"log"
	"os"
	"strconv"

	yapyap "yapyap/server"
)

func main() {
	jwtSec, found := os.LookupEnv("YP_JWT_SECRET")
	if !found {
		// probably test instace,  generate a random secret
		jwtSec = "XBPS_0xD09"
		log.Printf("YP_JWT_SECRET not found in environment variables, using unsafe default: %s", jwtSec)
	}
	jwtSecret := []byte(jwtSec)

	instaceName, found := os.LookupEnv("YP_INSTANCE_NAME")
	if !found {
		instaceName = "YapYap"
		log.Printf("YAPYAP_INSTANCE_NAME not found in environment variables, using default: %s", instaceName)
	}

	host, found := os.LookupEnv("YP_HOST")
	if !found {
		host = "localhost"
	}
	port, found := os.LookupEnv("YP_PORT")
	if !found {
		port = "8080"
	}

	portInt, err := strconv.Atoi(port)
	if err != nil {
		log.Fatalf("Invalid port number: %s", port)
	}

	server := yapyap.NewYapYap(instaceName, host, portInt, jwtSecret)
	server.Start()
}
