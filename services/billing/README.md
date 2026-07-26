# billing service (Phase 7)

Ports: **8020**

- Plans: `free` / `pro` / `scale`
- Usage meters (stubs): `build_minutes`, `runtime_hours`
- Consumes `jp.deploy` + `jp.build` Redis Streams into usage events
