# Prometheus metrics (uses Victoria/Metrics)

Usage: 
1. At first, we need create a new instance of metrics.Meter (interface: `metrics.Meter`).
2. The second step is passing the metrics.Metrics into the response/request middlewares.
3. Thirdly, the request/response middlewares should be added into the target HTTP server.
