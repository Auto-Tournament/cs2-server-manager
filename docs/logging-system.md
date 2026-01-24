# Command Logs

Every command is automatically logged for easy debugging.

## Quick Start

**View logs in TUI:**
- Go to **Tools** tab → **View recent command logs**

**View logs from terminal:**
```bash
csm logdir              # Show logs directory and recent logs
tail -f logs/csm.log    # Follow live logs
grep -i error logs/*.log # Search for errors
```

## Log Files

All logs are in `logs/` directory:
- `csm.log` - All commands
- `YYYY-MM-DD_HH-MM-SS_command-name.log` - Individual command logs

## Common Tasks

**Debug a failed command:**
```bash
csm logdir              # List recent logs
cat logs/<filename>     # View full log
```

**Monitor live operations:**
```bash
tail -f logs/csm.log
```

**Clean old logs:**
```bash
find logs/ -name "202*.log" -mtime +30 -delete
```

That's it! Every command you run is logged automatically.
