#!/usr/bin/env bash

###############################################################################
# Convert Map Thumbnails from vtex_c to PNG
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
OUTPUT_DIR="${PROJECT_ROOT}/map_thumbnails"

# Function to check if required tools are available
check_tools() {
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

# Function to install required tools
install_tools() {
  echo -e "${YELLOW}Installing required tools...${NC}"
  
  # Install Pillow
  if command -v pip3 >/dev/null 2>&1; then
    echo "Installing Pillow via pip3..."
    pip3 install Pillow --break-system-packages >/dev/null 2>&1 || pip3 install Pillow --user >/dev/null 2>&1
    if python3 -c "from PIL import Image" 2>/dev/null; then
      echo -e "${GREEN}✓ Pillow installed successfully${NC}"
      return 0
    fi
  fi
  
  echo -e "${RED}Failed to install Pillow${NC}"
  echo "Please install manually:"
  echo "  pip3 install Pillow --break-system-packages"
  return 1
}

# Function to convert vtex_c to PNG using Python
convert_vtex_to_png() {
  local vtex_file="$1"
  local output_file="$2"
  
  # Determine which Python to use
  local python_cmd=""
  if command -v python3 >/dev/null 2>&1; then
    python_cmd="python3"
  elif command -v python >/dev/null 2>&1; then
    python_cmd="python"
  else
    return 1
  fi
  
  # Try to extract image data from vtex_c
  # vtex_c files often contain DXT compressed textures or raw image data
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
        # Found embedded PNG, extract it
        png_data = data[png_start:]
        # Find PNG end (IEND chunk)
        png_end = png_data.find(b'IEND\xaeB`\x82') + 8
        if png_end > 8:
            png_data = png_data[:png_end]
            img = Image.open(io.BytesIO(png_data))
            img.save(output_file, 'PNG')
            return True
    
    # Try to find DDS signature (DDS header)
    dds_start = data.find(b'DDS ')
    if dds_start != -1:
        # Found DDS texture, would need DXT decompression
        # For now, skip - would need additional library
        return False
    
    # Try to find TGA signature
    # TGA files can start with various headers
    # Look for common TGA patterns
    if len(data) > 18:
        # Check if it might be a TGA file
        # TGA header is 18 bytes, then image data
        try:
            # Try to read as TGA
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
        print(f"Could not extract image from: {vtex_file}", file=sys.stderr)
        sys.exit(1)
except Exception as e:
    print(f"Error: {str(e)}", file=sys.stderr)
    sys.exit(1)
PYTHON_SCRIPT
  
  if "$python_cmd" "$py_script" "$vtex_file" "$output_file" 2>&1 | grep -v "Error\|Could not"; then
    rm -f "$py_script"
    return 0
  else
    rm -f "$py_script"
    return 1
  fi
}

# Function to convert using vrf_decompiler if available
convert_vtex_decompiler() {
  local vtex_file="$1"
  local output_file="$2"
  
  if command -v vrf_decompiler >/dev/null 2>&1; then
    # vrf_decompiler -i input.vtex_c -o output.png
    if vrf_decompiler -i "$vtex_file" -o "$output_file" 2>/dev/null; then
      return 0
    fi
  fi
  
  return 1
}

# Main conversion function
convert_thumbnails() {
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo -e "${CYAN}  Convert Map Thumbnails (vtex_c -> PNG)${NC}"
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo
  
  if [[ ! -d "$EXTRACTED_DIR" ]]; then
    echo -e "${RED}Error: Extracted CSGO directory not found at ${EXTRACTED_DIR}${NC}"
    echo "Please run the extraction script first (option 17 in manage.sh)"
    exit 1
  fi
  
  # Check for required tools
  if ! check_tools; then
    if ! install_tools; then
      echo
      echo -e "${RED}Cannot proceed without required tools.${NC}"
      echo "Please install: pip3 install Pillow --break-system-packages"
      exit 1
    fi
  fi
  
  # Create output directory
  mkdir -p "$OUTPUT_DIR"
  echo -e "${GREEN}Output directory: ${OUTPUT_DIR}${NC}"
  echo
  
  # Find all vtex_c files in 1080p screenshot folders
  echo -e "${BLUE}Scanning for map thumbnail files...${NC}"
  mapfile -t vtex_files < <(find "$EXTRACTED_DIR" -path "*/panorama/images/map_icons/screenshots/1080p/*.vtex_c" -type f 2>/dev/null | sort)
  
  if [[ ${#vtex_files[@]} -eq 0 ]]; then
    echo -e "${YELLOW}No vtex_c files found in 1080p screenshot folders${NC}"
    exit 0
  fi
  
  echo -e "${GREEN}Found ${#vtex_files[@]} thumbnail files${NC}"
  echo
  
  # Convert each file
  local success_count=0
  local fail_count=0
  
  for vtex_file in "${vtex_files[@]}"; do
    # Get the base name without extension
    local basename=$(basename "$vtex_file" .vtex_c)
    
    # Create output filename (replace _png with .png)
    local output_name="${basename/_png/}.png"
    local output_path="${OUTPUT_DIR}/${output_name}"
    
    # Skip if already converted
    if [[ -f "$output_path" ]]; then
      echo -e "${YELLOW}[SKIP]${NC} ${basename} (already converted)"
      continue
    fi
    
    echo -e "${BLUE}[CONVERT]${NC} ${basename}..."
    
    # Try vrf_decompiler first, then Python VRF
    if convert_vtex_decompiler "$vtex_file" "$output_path" 2>/dev/null; then
      echo -e "${GREEN}[OK]${NC} ${basename}"
      ((success_count++))
    elif convert_vtex_to_png "$vtex_file" "$output_path" 2>/dev/null; then
      echo -e "${GREEN}[OK]${NC} ${basename}"
      ((success_count++))
    else
      echo -e "${RED}[FAIL]${NC} ${basename}"
      ((fail_count++))
    fi
  done
  
  echo
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo -e "${GREEN}Conversion Complete!${NC}"
  echo -e "${CYAN}════════════════════════════════════════════════════════${NC}"
  echo
  echo "Summary:"
  echo "  Successfully converted: $success_count"
  echo "  Failed: $fail_count"
  echo "  Output directory: ${OUTPUT_DIR}"
  echo
}

# Run if executed directly
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  convert_thumbnails
fi

