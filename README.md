Lumo is a simple logger for Go, designed for hobby projects and small applications. It focuses on keeping logs readable and easy on the eyes without requiring complex configuration.

* **Easy to read:** Clear formatting with colors to separate levels, timestamps, and messages.
* **Non-blocking:** Logs are handled asynchronously in a background worker.
* **Simple context support:** Attach variables to errors to see exactly what data caused a crash.
* **Pretty traces:** Stack traces filter out Go runtime internals so you only see your code.

![Lumo Example Log Output](.github/example.png)

## Installation
```bash
go get [github.com/yourusername/lumo](https://github.com/yourusername/lumo)
```