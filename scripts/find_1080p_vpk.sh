#!/usr/bin/env bash

###############################################################################
# Find which VPK file contains the 1080p screenshot folder
###############################################################################

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
EXTRACTED_DIR="${PROJECT_ROOT}/extracted_csgo"
TARGET_FOLDER="panorama/images/map_icons/screenshots/1080p"

find_1080p_vpk() {
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo -e "${CYAN}  Find VPK Containing 1080p Screenshot Folder${NC}"
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo
  
  if [[ ! -d "$EXTRACTED_DIR" ]]; then
    echo -e "${RED}Error: Extracted CSGO directory not found at ${EXTRACTED_DIR}${NC}"
    echo "Please run the extraction script first (option 17 in manage.sh)"
    exit 1
  fi
  
  echo -e "${BLUE}Searching for 1080p folder in extracted VPKs...${NC}"
  echo
  
  # Find all directories matching the 1080p path
  local found_folders=()
  mapfile -t found_folders < <(find "$EXTRACTED_DIR" -type d -path "*/${TARGET_FOLDER}" 2>/dev/null)
  
  if [[ ${#found_folders[@]} -eq 0 ]]; then
    echo -e "${YELLOW}No 1080p folders found in extracted files${NC}"
    exit 0
  fi
  
  echo -e "${GREEN}Found ${#found_folders[@]} 1080p folder(s)${NC}"
  echo
  
  # For each folder, determine which VPK it came from
  for folder in "${found_folders[@]}"; do
    # Get the relative path from extracted_csgo
    local rel_path="${folder#$EXTRACTED_DIR/}"
    
    # Extract the VPK name (first directory component)
    local vpk_name=$(echo "$rel_path" | cut -d'/' -f1)
    
    # Find the original VPK file
    local vpk_file=""
    
    # Check if it's from a map VPK (in maps directory)
    if [[ "$vpk_name" =~ ^(de_|ar_|cs_|toolscene_|lobby_|graphics_|script_|smartprop_) ]]; then
      vpk_file="/home/cs2/master-install/game/csgo/maps/${vpk_name}.vpk"
    else
      # It's likely from pak01_dir or another VPK
      # Check if it's in pak01_dir
      if [[ "$rel_path" == pak01_dir/* ]]; then
        # Find which pak01 VPK contains this
        # pak01 files are numbered like pak01_001.vpk, pak01_002.vpk, etc.
        # We need to check the master install directory
        echo -e "${BLUE}Checking pak01 VPK files...${NC}"
        local pak01_vpks=($(find /home/cs2/master-install/game/csgo -name "pak01_*.vpk" -type f 2>/dev/null | sort))
        
        if [[ ${#pak01_vpks[@]} -gt 0 ]]; then
          echo -e "${GREEN}Found ${#pak01_vpks[@]} pak01 VPK files${NC}"
          echo -e "${YELLOW}The 1080p folder is likely in one of the pak01 VPK files${NC}"
          echo
          echo "To find the exact one, you would need to extract each pak01 VPK individually"
          echo "or check the pak01_dir.vpk file if it exists."
          echo
          vpk_file="pak01_dir.vpk (or one of pak01_*.vpk files)"
        else
          vpk_file="pak01_dir.vpk (not found in master install)"
        fi
      else
        vpk_file="Unknown VPK (${vpk_name})"
      fi
    fi
    
    echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
    echo -e "${GREEN}1080p Folder Found!${NC}"
    echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
    echo
    echo "Location:"
    echo "  ${folder}"
    echo
    echo "VPK Information:"
    echo "  Extracted from: ${vpk_name}"
    echo "  Original VPK: ${vpk_file}"
    echo
    
    # Count files in the folder
    local file_count=$(find "$folder" -type f 2>/dev/null | wc -l)
    local vtex_count=$(find "$folder" -type f -name "*.vtex_c" 2>/dev/null | wc -l)
    
    echo "Contents:"
    echo "  Total files: ${file_count}"
    echo "  vtex_c files: ${vtex_count}"
    echo
    
    # Show some example files
    echo "Example files:"
    find "$folder" -type f -name "*.vtex_c" 2>/dev/null | head -5 | while read -r file; do
      echo "  $(basename "$file")"
    done
    echo
  done
  
  # Summary
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo -e "${GREEN}Summary${NC}"
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo
  echo "The 1080p screenshot folder is most likely in:"
  echo "  ${GREEN}pak01_dir.vpk${NC} or one of the ${GREEN}pak01_*.vpk${NC} files"
  echo
  echo "These are the main game resource VPK files that contain"
  echo "UI elements, materials, and other shared resources."
  echo
}

# Run if executed directly
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  find_1080p_vpk
fi

