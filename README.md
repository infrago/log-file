# log-file
file driver for infrago/log.

settings:

- `store`: root path, default `store/log`
- `output`: merged output file
- `<level>`: per-level file path, e.g. `error = "error/error.log"`
- `maxsize`: rotate by size, e.g. `64MB`
- `slice`: rotate by time window: `hour|day|month|year`
- `maxline`: rotate by line count
- `maxfiles`: keep latest N rotated files
- `maxage`: delete rotated files older than duration (e.g. `7d`, `72h`)
- `compress`: compress rotated files to `.gz` asynchronously
infrago file log driver.
