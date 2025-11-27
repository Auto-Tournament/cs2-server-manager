# How to Enable Demo Upload in MatchZy

This guide explains how to configure MatchZy to record and upload match demos to your API.

## Overview

MatchZy automatically records demos for each map in a match series. For example, in a Best-of-3 (BO3) match, it will create **3 separate demo files** - one for each map. Each demo is:
1. **Always saved locally** to the server's disk
2. **Optionally uploaded** to your API endpoint (if configured)

## Requirements

### 1. CS2 Server Configuration

**GOTV must be enabled** for demo recording to work. Add these to your server's launch parameters or server.cfg:

```
tv_enable 1
```

**Optional but recommended:**
```
tv_delay 90          # Delay in seconds for GOTV broadcast (prevents ghosting)
tv_port 27020       # GOTV port (default is 27020)
```

**Important Notes:**
- `tv_enable 1` is **REQUIRED** - without it, demos cannot be recorded
- Do NOT change `tv_delay` or `tv_enable` in warmup.cfg or live.cfg - this can cause demo recording issues
- Set `tv_delay` in your main server configuration file

### 2. MatchZy Configuration

Edit `cfg/MatchZy/config.cfg` and set:

```cfg
// Enable demo recording (default: true)
matchzy_demo_recording_enabled true

// Path where demos are saved locally (default: MatchZy/)
// This is ALWAYS used - demos are always saved locally
matchzy_demo_path MatchZy/

// Demo filename format
matchzy_demo_name_format "{TIME}_{MATCH_ID}_{MAP}_{TEAM1}_vs_{TEAM2}"

// API endpoint for uploading demos (REQUIRED for upload)
// Wrap the URL in double quotes
matchzy_demo_upload_url "https://your-api.com/api/demos/upload"

// Optional: Custom HTTP headers for authentication
matchzy_demo_upload_header_key "Authorization"
matchzy_demo_upload_header_value "Bearer your-token-here"
```

## How It Works

### Demo Recording Lifecycle

1. **Match Starts**: When all teams ready up and the match goes live, `StartDemoRecording()` is called
2. **Recording**: The server records the demo using `tv_record` command
3. **Map Ends**: When a map ends, `StopDemoRecording()` is called
4. **File Write**: The server waits 15 seconds for the demo file to be written to disk
5. **Upload**: If `matchzy_demo_upload_url` is configured, the demo is uploaded to your API
6. **Next Map**: For BO3/BO5, a new demo recording starts for the next map

### Demo File Details

- **One demo per map**: Each map gets its own `.dem` file
- **BO3 example**: Creates 3 separate demo files (map 0, map 1, map 2)
- **File location**: Saved to `{csgo_directory}/{demo_path}/{filename}.dem`
- **File format**: Raw `.dem` file (not zipped, despite what some docs may say)

### Upload Mechanism

- **Upload type**: Single file upload (not streaming)
- **HTTP method**: POST
- **Content type**: `application/octet-stream`
- **File handling**: Entire file is read into memory and sent as POST body

### HTTP Headers Sent

Your API will receive these headers with each upload:

```
MatchZy-FileName: {demo_filename}.dem
MatchZy-MatchId: {match_id}
MatchZy-MapNumber: {map_number}  # Zero-indexed (0, 1, 2...)
MatchZy-RoundNumber: {total_rounds}
```

For Get5 compatibility, these headers are also sent:
```
Get5-FileName: {demo_filename}.dem
Get5-MatchId: {match_id}
Get5-MapNumber: {map_number}
Get5-RoundNumber: {total_rounds}
```

Plus any custom headers you configure via `matchzy_demo_upload_header_key` and `matchzy_demo_upload_header_value`.

## Local Storage vs API Upload

**Both are supported simultaneously:**

- ✅ **Local storage**: Always enabled (demos are always saved to disk)
- ✅ **API upload**: Optional (only if `matchzy_demo_upload_url` is set)

You can have:
- Only local storage (no upload URL set)
- Only API upload (upload URL set, but you can delete local files after upload)
- Both local storage AND API upload (recommended for redundancy)

## API Endpoint Requirements

Your API endpoint must:

1. Accept POST requests
2. Handle `application/octet-stream` content type
3. Read the request body as binary data
4. Extract metadata from HTTP headers
5. Return HTTP 200-299 for success, or 400+ for errors

### Example API Endpoint (Node.js/Express)

```javascript
const express = require('express');
const fs = require('fs');
const path = require('path');

const app = express();

app.post('/api/demos/upload', express.raw({ type: 'application/octet-stream', limit: '500mb' }), (req, res) => {
  const filename = req.headers['matchzy-filename'];
  const matchId = req.headers['matchzy-matchid'];
  const mapNumber = req.headers['matchzy-mapnumber'];
  const roundNumber = req.headers['matchzy-roundnumber'];

  console.log(`Received demo: ${filename} for match ${matchId}, map ${mapNumber}`);

  // Create directory for this match
  const matchDir = path.join(__dirname, 'demos', matchId);
  if (!fs.existsSync(matchDir)) {
    fs.mkdirSync(matchDir, { recursive: true });
  }

  // Save the demo file
  const filePath = path.join(matchDir, filename);
  fs.writeFileSync(filePath, req.body);

  console.log(`Demo saved: ${filePath}`);
  res.status(200).json({ success: true, message: 'Demo uploaded successfully' });
});

app.listen(3000);
```

## Debugging

### Check Server Logs

MatchZy now includes extensive debug logging. Look for these log entries:

**When recording starts:**
```
[StartDemoRecording] Starting demo recording:
[StartDemoRecording]   - Demo file: {filename}
[StartDemoRecording]   - Relative path: {path}
[StartDemoRecording]   - Full path: {full_path}
[StartDemoRecording]   - GOTV enabled: true/false
```

**When recording stops:**
```
[StopDemoRecording] Going to stop demorecording in {delay}s
[StopDemoRecording] Demo info - MatchId: {id}, MapNumber: {num}, Rounds: {rounds}
[StopDemoRecording] Upload URL configured: YES/NO
```

**During upload:**
```
[UploadFileAsync] ===== Starting demo upload =====
[UploadFileAsync] File found. Size: {size} MB
[UploadFileAsync] ===== Upload SUCCESS =====
```

### Common Issues

**Issue: "Demo recording is disabled"**
- Solution: Set `matchzy_demo_recording_enabled true` in config.cfg

**Issue: "tv_enable is 0" warning**
- Solution: Add `tv_enable 1` to your server launch parameters or server.cfg

**Issue: "File not found" during upload**
- Solution: Check that GOTV is enabled and the demo was actually recorded
- Check the file path in logs
- Ensure the server has write permissions to the demo directory

**Issue: Upload fails with HTTP error**
- Solution: Check your API endpoint is accessible from the server
- Verify the URL is correct and wrapped in quotes
- Check API logs for errors
- Ensure your API can handle large file uploads (demos can be 100+ MB)

**Issue: No upload happening**
- Solution: Verify `matchzy_demo_upload_url` is set correctly in config.cfg
- Check that the URL is wrapped in double quotes
- Restart the server after changing config

## Verification Checklist

- [ ] `tv_enable 1` is set in server config
- [ ] `matchzy_demo_recording_enabled true` in config.cfg
- [ ] `matchzy_demo_upload_url` is set with a valid URL (in quotes)
- [ ] API endpoint is accessible and accepts POST requests
- [ ] Server logs show "Starting demo recording" when match starts
- [ ] Server logs show "Upload SUCCESS" after map ends
- [ ] Demo files appear in local directory
- [ ] Demo files appear on your API server

## Summary

- **Demos per match**: One demo per map (BO3 = 3 demos, BO5 = 5 demos)
- **Storage**: Always saved locally, optionally uploaded to API
- **Upload type**: Single file POST request (not streaming)
- **Requirements**: `tv_enable 1` + `matchzy_demo_upload_url` configured
- **Debug**: Check server logs for detailed upload information

