---
title: Related Projects
---

# Related Projects

CS2 Server Manager is designed to work closely with the MatchZy ecosystem to provide a full tournament-ready stack for Counter-Strike 2.

This page gives a quick overview of the two primary related projects:

- **MatchZy Auto Tournament**
- **MatchZy Enhanced**

Use this as a reference when deciding how to integrate them with your CS2 Server Manager installation.

## MatchZy Auto Tournament

**URL:** [https://mat.sivert.io](https://mat.sivert.io)

**What it is:**  
MatchZy Auto Tournament is a web UI and API for automating CS2 tournaments end-to-end.

**Key capabilities:**

- **Tournament orchestration:** Create brackets, schedule matches, and manage progress from a central dashboard.
- **Server integration:** Connect to servers provisioned by CS2 Server Manager for automatic match assignment.
- **Automation hooks:** Start/stop matches, update scores, and manage vetoes via API calls.
- **Spectator & admin tooling:** Stream-friendly workflows and clearer visibility for admins.

**How it works with CS2 Server Manager:**

- CS2 Server Manager provides a pool of ready-to-use CS2 servers.
- MatchZy Auto Tournament talks to those servers to run matches with minimal manual intervention.

If you’re running structured tournaments (weekly cups, leagues, LANs), you almost certainly want this on top of CS2 Server Manager.

## MatchZy Enhanced

**URL:** [https://github.com/sivert-io/MatchZy-Enhanced](https://github.com/sivert-io/MatchZy-Enhanced)

**What it is:**  
MatchZy Enhanced is an extended MatchZy plugin that runs inside your CS2 servers to automate in-server workflows.

**Key capabilities:**

- **In-server automation:** Handles ready checks, knife rounds, side selection, and warmups.
- **Veto and map selection:** Guides players through veto/map-pick flows directly from the server.
- **Match state control:** Starts, pauses, and resumes matches based on tournament state or admin commands.
- **Better quality-of-life:** Cleaner messages, clearer states, and improved UX for both players and admins.

**How it works with CS2 Server Manager:**

- CS2 Server Manager installs and manages MatchZy Enhanced as part of its plugin stack.
- When combined with MatchZy Auto Tournament, you get:
  - Automated server spin-up and maintenance.
  - Automated match orchestration from the web.
  - Automated in-server flow once players connect.

If you are using CS2 Server Manager in a competitive or tournament context, MatchZy Enhanced is the recommended plugin layer.
