#!/usr/bin/env bash

###############################################################################
# Extract Map Data from VPK Files
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
MASTER_INSTALL_DIR="/home/cs2/master-install"
CSGO_DIR="${MASTER_INSTALL_DIR}/game/csgo"
MAPS_DIR="${CSGO_DIR}/maps"
OUTPUT_DIR="${PROJECT_ROOT}/extracted_csgo"
THUMBNAILS_OUTPUT_DIR="${PROJECT_ROOT}/map_thumbnails"
TARGET_FOLDER="panorama/images/map_icons/screenshots/1080p"

# Check if master install exists
if [[ ! -d "$MASTER_INSTALL_DIR" ]]; then
  echo -e "${RED}Error: Master install not found at ${MASTER_INSTALL_DIR}${NC}"
  exit 1
fi

# Check if csgo directory exists
if [[ ! -d "$CSGO_DIR" ]]; then
  echo -e "${RED}Error: CSGO directory not found at ${CSGO_DIR}${NC}"
  exit 1
fi

# Function to check if Python vpk module is available
check_vpk_tool() {
  # Check if we have a venv python set
  if [[ -n "$VPK_VENV_PYTHON" && -f "$VPK_VENV_PYTHON" ]]; then
    if "$VPK_VENV_PYTHON" -c "import vpk" 2>/dev/null; then
      return 0
    fi
  fi
  
  # Check for venv in script directory
  local venv_dir="${SCRIPT_DIR}/.vpk_venv"
  if [[ -f "$venv_dir/bin/python" ]]; then
    if "$venv_dir/bin/python" -c "import vpk" 2>/dev/null; then
      export VPK_VENV_PYTHON="$venv_dir/bin/python"
      return 0
    fi
  fi
  
  # Try python3
  if python3 -c "import vpk" 2>/dev/null; then
    return 0
  fi
  
  # Try with python instead of python3
  if python -c "import vpk" 2>/dev/null; then
    return 0
  fi
  
  return 1
}

# Function to install Python vpk module
install_vpk_tool() {
  echo -e "${YELLOW}Python vpk module not found. Attempting to install...${NC}"
  
  # Try pip3 with --user flag first
  if command -v pip3 >/dev/null 2>&1; then
    echo "Attempting to install vpk via pip3 --user..."
    pip3 install vpk --user >/dev/null 2>&1
    if check_vpk_tool; then
      echo -e "${GREEN}✓ vpk module installed successfully (user install)${NC}"
      return 0
    fi
  fi
  
  # Try pip3 with --break-system-packages flag (for PEP 668 systems like Debian/Ubuntu)
  if command -v pip3 >/dev/null 2>&1; then
    echo -e "${YELLOW}Trying pip3 with --break-system-packages flag (for system-wide install)...${NC}"
    pip3 install vpk --break-system-packages >/dev/null 2>&1
    if check_vpk_tool; then
      echo -e "${GREEN}✓ vpk module installed successfully (system-wide)${NC}"
      return 0
    fi
  fi
  
  # Try creating a virtual environment as last resort
  local venv_dir="${SCRIPT_DIR}/.vpk_venv"
  if command -v python3 >/dev/null 2>&1; then
    echo "Creating virtual environment for vpk (this may take a moment)..."
    if python3 -m venv "$venv_dir" 2>/dev/null; then
      # Upgrade pip in venv first
      "$venv_dir/bin/pip" install --upgrade pip --quiet >/dev/null 2>&1 || true
      # Install vpk in venv
      if "$venv_dir/bin/pip" install vpk --quiet >/dev/null 2>&1; then
        # Verify installation
        if "$venv_dir/bin/python" -c "import vpk" 2>/dev/null; then
          echo -e "${GREEN}✓ vpk module installed in virtual environment${NC}"
          # Export venv path for use in extraction
          export VPK_VENV_PYTHON="$venv_dir/bin/python"
          return 0
        fi
      fi
    fi
  fi
  
  echo -e "${RED}Failed to install vpk module automatically${NC}"
  echo
  echo "Please install manually using one of these methods:"
  echo "  1. pip3 install vpk --break-system-packages"
  echo "  2. pip3 install vpk --user"
  echo "  3. pipx install vpk"
  echo "  4. Create a virtual environment: python3 -m venv venv && venv/bin/pip install vpk"
  return 1
}

# Function to extract VPK using Python
extract_vpk_python() {
  local vpk_file="$1"
  local output_path="$2"
  
  # Determine which Python to use
  local python_cmd=""
  
  # Check for venv python first
  if [[ -n "$VPK_VENV_PYTHON" && -f "$VPK_VENV_PYTHON" ]]; then
    python_cmd="$VPK_VENV_PYTHON"
  elif [[ -f "${SCRIPT_DIR}/.vpk_venv/bin/python" ]]; then
    python_cmd="${SCRIPT_DIR}/.vpk_venv/bin/python"
  elif command -v python3 >/dev/null 2>&1 && python3 -c "import vpk" 2>/dev/null; then
    python_cmd="python3"
  elif command -v python >/dev/null 2>&1 && python -c "import vpk" 2>/dev/null; then
    python_cmd="python"
  else
    return 1
  fi
  
  # Use a temporary Python script file to avoid heredoc issues
  local py_script=$(mktemp)
  cat > "$py_script" <<'PYTHON_SCRIPT'
import vpk
import os
import sys

try:
    vpk_file = sys.argv[1]
    output_path = sys.argv[2]
    
    # Open the VPK file
    pak = vpk.open(vpk_file)
    
    # Extract all files
    file_count = 0
    for filepath in pak:
        # Get the file data
        file_data = pak[filepath]
        
        # Create output file path
        full_output_path = os.path.join(output_path, filepath)
        
        # Create directory if needed
        dir_path = os.path.dirname(full_output_path)
        if dir_path:
            os.makedirs(dir_path, exist_ok=True)
        
        # Write file
        with open(full_output_path, 'wb') as f:
            f.write(file_data.read())
        file_count += 1
    
    print(f"Extracted {file_count} files from {vpk_file}")
    sys.exit(0)
except Exception as e:
    print(f"Error: {str(e)}", file=sys.stderr)
    sys.exit(1)
PYTHON_SCRIPT
  
  # Run the Python script and capture output
  local output
  if output=$("$python_cmd" "$py_script" "$vpk_file" "$output_path" 2>&1); then
    echo "$output"
    rm -f "$py_script"
    return 0
  else
    echo "$output" >&2
    rm -f "$py_script"
    return 1
  fi
}

# Main extraction function
# Function to check if required conversion tools are available
check_conversion_tools() {
  # Check for Python
  if ! command -v python3 >/dev/null 2>&1 && ! command -v python >/dev/null 2>&1; then
    return 1
  fi
  
  # Check for PIL/Pillow
  if ! python3 -c "from PIL import Image" 2>/dev/null && ! python -c "from PIL import Image" 2>/dev/null; then
    return 1
  fi
  
  return 0
}

# Function to install conversion tools
install_conversion_tools() {
  echo -e "${YELLOW}Installing Pillow for image conversion...${NC}"
  
  if command -v pip3 >/dev/null 2>&1; then
    pip3 install Pillow --break-system-packages >/dev/null 2>&1 || pip3 install Pillow --user >/dev/null 2>&1
    if python3 -c "from PIL import Image" 2>/dev/null; then
      echo -e "${GREEN}✓ Pillow installed successfully${NC}"
      return 0
    fi
  fi
  
  echo -e "${RED}Failed to install Pillow${NC}"
  echo "Please install manually: pip3 install Pillow --break-system-packages"
  return 1
}

# Function to convert vtex_c to PNG
convert_vtex_to_png() {
  local vtex_file="$1"
  local output_file="$2"
  
  local python_cmd=""
  if command -v python3 >/dev/null 2>&1; then
    python_cmd="python3"
  elif command -v python >/dev/null 2>&1; then
    python_cmd="python"
  else
    return 1
  fi
  
  local py_script=$(mktemp)
  cat > "$py_script" <<'PYTHON_SCRIPT'
import sys
import struct
from PIL import Image
import io

def extract_vtex_image(vtex_file, output_file):
    """Try to extract image from vtex_c file"""
    with open(vtex_file, 'rb') as f:
        data = f.read()
    
    # Try to find PNG signature (89 50 4E 47)
    png_start = data.find(b'\x89PNG')
    if png_start != -1:
        png_data = data[png_start:]
        png_end = png_data.find(b'IEND\xaeB`\x82') + 8
        if png_end > 8:
            png_data = png_data[:png_end]
            img = Image.open(io.BytesIO(png_data))
            img.save(output_file, 'PNG')
            return True
    
    # Try to find DDS signature (DDS header)
    dds_start = data.find(b'DDS ')
    if dds_start != -1:
        return False
    
    # Try to read as TGA or other image format
    if len(data) > 18:
        try:
            img = Image.open(io.BytesIO(data))
            img.save(output_file, 'PNG')
            return True
        except:
            pass
    
    return False

try:
    vtex_file = sys.argv[1]
    output_file = sys.argv[2]
    
    if extract_vtex_image(vtex_file, output_file):
        print(f"Converted: {vtex_file} -> {output_file}")
        sys.exit(0)
    else:
        sys.exit(1)
except Exception as e:
    print(f"Error: {str(e)}", file=sys.stderr)
    sys.exit(1)
PYTHON_SCRIPT
  
  if "$python_cmd" "$py_script" "$vtex_file" "$output_file" 2>&1 | grep -q "Converted"; then
    rm -f "$py_script"
    return 0
  else
    rm -f "$py_script"
    return 1
  fi
}


extract_map_data() {
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo -e "${CYAN}  Extract & Convert Map Thumbnails${NC}"
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo
  
  # Check for VPK tool
  if ! check_vpk_tool; then
    if ! install_vpk_tool; then
      echo
      echo -e "${RED}Cannot proceed without vpk extraction tool.${NC}"
      echo "Please install manually:"
      echo "  pip3 install vpk"
      exit 1
    fi
  fi
  
  # Create output directory
  mkdir -p "$OUTPUT_DIR"
  echo -e "${GREEN}Output directory: ${OUTPUT_DIR}${NC}"
  echo
  
  # Always extract only 1080p VPKs (default behavior)
  local only_1080p_vpks=1
  
  if [[ $only_1080p_vpks -eq 1 ]]; then
    # Only extract VPKs that contain 1080p folders
    echo -e "${BLUE}Extracting only VPKs containing 1080p screenshot folders...${NC}"
    echo
    
    # Only extract pak01_dir.vpk (contains all working thumbnails)
    local target_vpks=(
      "${CSGO_DIR}/pak01_dir.vpk"
    )
    
    echo -e "${GREEN}Target VPK:${NC}"
    echo "  - pak01_dir.vpk (88 vtex_c files)"
    echo
    
    # Filter to only existing files
    local existing_vpks=()
    for vpk in "${target_vpks[@]}"; do
      if [[ -f "$vpk" ]]; then
        existing_vpks+=("$vpk")
      fi
    done
    
    if [[ ${#existing_vpks[@]} -eq 0 ]]; then
      echo -e "${RED}None of the target VPK files found${NC}"
      echo "Looking for:"
      printf "  %s\n" "${target_vpks[@]}"
      exit 1
    fi
    
    mapfile -t vpk_files < <(printf '%s\n' "${existing_vpks[@]}" | sort)
    echo -e "${GREEN}Found ${#vpk_files[@]} target VPK file(s)${NC}"
    echo "Files to extract:"
    for vpk in "${vpk_files[@]}"; do
      echo "  $(basename "$vpk")"
    done
    echo
  else
    # Extract ALL .vpk files recursively in game/csgo
    echo -e "${BLUE}Scanning for ALL .vpk files in ${CSGO_DIR}...${NC}"
    echo -e "${YELLOW}This may take a while - extracting all VPK files...${NC}"
    echo
    
    mapfile -t vpk_files < <(find "$CSGO_DIR" -type f -name "*.vpk" 2>/dev/null | sort)
    
    if [[ ${#vpk_files[@]} -eq 0 ]]; then
      echo -e "${YELLOW}No .vpk files found in ${CSGO_DIR}${NC}"
      exit 0
    fi
    
    echo -e "${GREEN}Found ${#vpk_files[@]} .vpk file(s)${NC}"
    echo
  fi
  
  # Check if 1080p folder already exists before extraction
  local target_folder="panorama/images/map_icons/screenshots/1080p"
  local found_1080p_before=0
  if find "$OUTPUT_DIR" -type d -path "*/${target_folder}" 2>/dev/null | grep -q .; then
    found_1080p_before=1
    echo -e "${YELLOW}Note: 1080p folder already exists before extraction${NC}"
  fi
  
  # Extract each VPK file
  local success_count=0
  local fail_count=0
  local vpk_with_1080p=""
  
  for vpk_file in "${vpk_files[@]}"; do
    # Get the base name without extension
    local vpk_basename=$(basename "$vpk_file" .vpk)
    
    # Create output directory for this VPK
    local extract_path="${OUTPUT_DIR}/${vpk_basename}"
    
    # Skip if already extracted (optional: add --force flag later)
    if [[ -d "$extract_path" ]]; then
      echo -e "${YELLOW}[SKIP]${NC} ${vpk_basename} (already extracted)"
      # Check if this VPK contains the 1080p folder
      if [[ -z "$vpk_with_1080p" ]] && find "$extract_path" -type d -path "*/${target_folder}" 2>/dev/null | grep -q .; then
        vpk_with_1080p="$vpk_file"
        echo -e "${GREEN}[FOUND]${NC} 1080p folder found in: ${vpk_basename}"
      fi
      continue
    fi
    
    echo -e "${BLUE}[EXTRACT]${NC} ${vpk_basename}..."
    mkdir -p "$extract_path"
    
    # Check if 1080p folder exists before this extraction
    local had_1080p_before=0
    if find "$OUTPUT_DIR" -type d -path "*/${target_folder}" 2>/dev/null | grep -q .; then
      had_1080p_before=1
    fi
    
    if extract_vpk_python "$vpk_file" "$extract_path"; then
      echo -e "${GREEN}[OK]${NC} ${vpk_basename}"
      ((success_count++))
      
      # Check if 1080p folder appeared after this extraction
      if [[ $had_1080p_before -eq 0 ]] && find "$OUTPUT_DIR" -type d -path "*/${target_folder}" 2>/dev/null | grep -q .; then
        # Check if it's in this specific VPK's extraction
        if find "$extract_path" -type d -path "*/${target_folder}" 2>/dev/null | grep -q .; then
          vpk_with_1080p="$vpk_file"
          echo -e "${GREEN}[FOUND]${NC} 1080p folder appeared in: ${vpk_basename}"
        fi
      fi
    else
      echo -e "${RED}[FAIL]${NC} ${vpk_basename}"
      # Remove failed extraction directory
      rm -rf "$extract_path"
      ((fail_count++))
    fi
  done
  
  echo
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo -e "${GREEN}Extraction Complete!${NC}"
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo
  echo "Summary:"
  echo "  Successfully extracted: $success_count"
  echo "  Failed: $fail_count"
  echo "  Output directory: ${OUTPUT_DIR}"
  echo
  
  # Report which VPK contains the 1080p folder
  if [[ -n "$vpk_with_1080p" ]]; then
    echo
    echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
    echo -e "${GREEN}1080p Folder Location Found!${NC}"
    echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
    echo
    echo -e "${GREEN}The 1080p screenshot folder is in:${NC}"
    echo "  VPK File: ${vpk_with_1080p}"
    echo "  VPK Name: $(basename "$vpk_with_1080p")"
    echo
    echo "Extracted location:"
    local vpk_basename=$(basename "$vpk_with_1080p" .vpk)
    find "${OUTPUT_DIR}/${vpk_basename}" -type d -path "*/panorama/images/map_icons/screenshots/1080p" 2>/dev/null | head -1 | while read -r folder; do
      echo "  ${folder}"
      echo
      echo "Files in 1080p folder:"
      find "$folder" -type f -name "*.vtex_c" 2>/dev/null | wc -l | xargs -I {} echo "  {} vtex_c files"
    done
    echo
  else
    echo
    echo -e "${YELLOW}Note: 1080p folder location not detected during extraction${NC}"
    echo "It may have existed before extraction, or is in a skipped VPK file."
    echo
  fi
  
  # Convert thumbnails and cleanup
  echo
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo -e "${CYAN}  Converting Thumbnails to PNG${NC}"
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo
  
  # Check for conversion tools
  if ! check_conversion_tools; then
    if ! install_conversion_tools; then
      echo -e "${YELLOW}Skipping conversion - tools not available${NC}"
    fi
  fi
  
  # Do conversion if tools are available
  if check_conversion_tools; then
    mkdir -p "$THUMBNAILS_OUTPUT_DIR"
    
    mapfile -t vtex_files < <(find "$OUTPUT_DIR" -path "*/${TARGET_FOLDER}/*.vtex_c" -type f 2>/dev/null | sort)
    
    if [[ ${#vtex_files[@]} -gt 0 ]]; then
      echo -e "${GREEN}Found ${#vtex_files[@]} thumbnail files${NC}"
      echo
      
      local convert_success=0
      local convert_fail=0
      
      for vtex_file in "${vtex_files[@]}"; do
        local basename=$(basename "$vtex_file" .vtex_c)
        
        # Skip numbered variants (e.g., de_dust2_1_png.vtex_c -> de_dust2_1.png)
        # Pattern: ends with _<number>_png (e.g., _1_png, _2_png)
        if [[ "$basename" =~ _[0-9]+_png$ ]]; then
          continue
        fi
        
        # Also skip if it ends with _png followed by a hash (e.g., _png_4f4cc8b5)
        if [[ "$basename" =~ _png_[a-f0-9]+$ ]]; then
          continue
        fi
        
        local output_name="${basename/_png/}.png"
        local output_path="${THUMBNAILS_OUTPUT_DIR}/${output_name}"
        
        if [[ -f "$output_path" ]]; then
          continue
        fi
        
        echo -e "${BLUE}[CONVERT]${NC} ${basename}..."
        if convert_vtex_to_png "$vtex_file" "$output_path" 2>/dev/null; then
          echo -e "${GREEN}[OK]${NC} ${basename}"
          ((convert_success++))
        else
          echo -e "${RED}[FAIL]${NC} ${basename}"
          ((convert_fail++))
        fi
      done
      
      echo
      echo -e "${GREEN}Converted: $convert_success, Failed: $convert_fail${NC}"
      echo -e "${GREEN}Thumbnails saved to: ${THUMBNAILS_OUTPUT_DIR}${NC}"
    fi
  fi
  
  # Cleanup numbered variant PNG files
  echo
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo -e "${CYAN}  Removing Numbered Variant Files${NC}"
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo
  
  local removed_count=0
  for png_file in "${THUMBNAILS_OUTPUT_DIR}"/*.png; do
    if [[ -f "$png_file" ]]; then
      local filename=$(basename "$png_file" .png)
      # Remove files with _<number> pattern (e.g., de_dust2_1.png)
      if [[ "$filename" =~ _[0-9]+$ ]]; then
        echo "Removing: $(basename "$png_file")"
        rm -f "$png_file"
        ((removed_count++))
      fi
    fi
  done
  
  if [[ $removed_count -gt 0 ]]; then
    echo
    echo -e "${GREEN}Removed ${removed_count} numbered variant files${NC}"
  else
    echo "No numbered variants found to remove"
  fi
  
  # Cleanup extracted VPK folders
  echo
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo -e "${CYAN}  Cleaning Up Extracted Files${NC}"
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo
  
  local cleanup_count=0
  for vpk_file in "${vpk_files[@]}"; do
    local vpk_basename=$(basename "$vpk_file" .vpk)
    local extract_path="${OUTPUT_DIR}/${vpk_basename}"
    
    if [[ -d "$extract_path" ]]; then
      echo "Removing: ${extract_path}"
      rm -rf "$extract_path"
      ((cleanup_count++))
    fi
  done
  
  echo
  echo -e "${GREEN}Cleaned up ${cleanup_count} extracted VPK folders${NC}"
  echo
  
  echo
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo -e "${GREEN}All Done!${NC}"
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo
  echo "Map thumbnails are available at:"
  echo "  ${THUMBNAILS_OUTPUT_DIR}"
  echo
}

# Run if executed directly
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  extract_map_data
fi

