#!/usr/bin/env python3

# Copyright 2025 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import os
import fnmatch
import argparse
import datetime

# The license header to apply
APACHE_HEADER = """Copyright {year} The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
"""

# Mapping of file extensions to their comment syntax
# (line_prefix, block_start, block_end)
COMMENT_STYLES = {
    ".go": ("// ", None, None),
    ".sh": ("# ", None, None),
    ".py": ("# ", None, None),
    ".js": ("// ", None, None),
    ".ts": ("// ", None, None),
    ".java": ("// ", None, None),
    ".scala": ("// ", None, None),
    ".c": ("// ", None, None),
    ".h": ("// ", None, None),
    ".cpp": ("// ", None, None),
    ".tf": ("# ", None, None),
    # Block comments for file types that support them
    ".css": (None, "/*", " */"),
    ".xml": (None, "<!--", "-->"),
    ".html": (None, "<!--", "-->"),
}

# Default glob patterns to exclude, relative to the root directory
DEFAULT_EXCLUDES = [
    ".git/**",
    ".idea/**",
    "__pycache__/**",
    "node_modules/**",
    "vendor/**",
    "**/*.yaml",
    "**/*.yml",
    "**/LICENSE",
    "**/*.md",
    "**/OWNERS",
    "**/SECURITY_CONTACTS",
    "go.mod",
    "go.sum",
    "*.json",
    "*.pyc",
    "*.so",
    "*.o",
    "*.a",
    "*.dll",
    "*.exe",
    "*.jar",
    "*.class",
    "*.zip",
    "*.tar.gz",
    "*.tgz",
    "*.rar",
    "*.7z",
    "*.log",
    "*.sum",
    "*.DS_Store",
]

def file_extension_magic(file_path):
    """Tries to determine the file type, as encoded by a typical extension."""
    # Default to the file extension
    _, ext = os.path.splitext(file_path)
    if ext:
        return ext
    # Look for a shebang line
    with open(file_path, 'r', encoding='utf-8') as f:
        # Read the first 4k of the file, which should be enough for any header.
        try:
            content = f.read(4096)
        except UnicodeDecodeError:
            # Likely a binary file
            return None
        # First line is shebang (e.g., #!/usr/bin/env python)
        first_line = content.split('\n', 1)[0]
        if first_line.startswith("#!"):
            if "python" in first_line:
                return ".py"
            if "bash" in first_line or "sh" in first_line:
                return ".sh"
            print((f"unknown shebang in {file_path}: {first_line}"))
    return None

def get_comment_style(file_extension):
    """Gets the comment style for a file based on its extension."""
    return COMMENT_STYLES.get(file_extension)

def format_header(header_text, style):
    """Formats the header text with the correct comment style."""
    line_prefix, block_start, block_end = style

    # Add a space for line prefixes if they don't have one
    if line_prefix and not line_prefix.endswith(' '):
        line_prefix += ' '

    header_lines = header_text.strip().split('\n')

    if line_prefix:
        # Handle empty lines in header correctly
        formatted_lines = [f"{line_prefix}{line}".rstrip() if line else line_prefix.rstrip() for line in header_lines]
        return '\n'.join(formatted_lines) + '\n\n'

    if block_start and block_end:
        # Handle block comments
        formatted_header = f"{block_start}\n"
        formatted_header += '\n'.join(f" {line}".rstrip() if line else "" for line in header_lines)
        formatted_header += f"\n{block_end}\n\n"
        return formatted_header

    return None


def has_license_header(file_path):
    """Checks if a file already has an Apache license header."""
    try:
        with open(file_path, 'r', encoding='utf-8') as f:
            # Read the first 4k of the file, which should be enough for any header.
            content = f.read(4096)
            if not "Licensed under the Apache License, Version 2.0" in content:
                return False
            if not "The Kubernetes Authors" in content:
                return False
            return True
    except Exception as e:
        # print(f"Could not read file {file_path}: {e}")
        return True # Skip file on error


def apply_license_header(file_path, header_text, dry_run=False):
    """Applies the license header to a single file if it doesn't have one."""

    file_extension = file_extension_magic(file_path)
    if not file_extension:
        # print(f"Skipping (unknown file type): {file_path}")
        return


    if has_license_header(file_path):
        # print(f"Skipping (header exists): {file_path}")
        return

    style = get_comment_style(file_extension)
    if not style:
        # print(f"Skipping (unsupported extension): {file_path}")
        return

    formatted_header = format_header(header_text, style)
    if not formatted_header:
        # print(f"Skipping (could not format header): {file_path}")
        return

    print(f"Applying header to: {file_path}")
    if not dry_run:
        try:
            with open(file_path, 'r+', encoding='utf-8') as f:
                content = f.read()
                f.seek(0, 0)
                # Handle shebangs (e.g., #!/usr/bin/env python)
                if content.startswith("#!"):
                    lines = content.split('\n', 1)
                    shebang = lines[0]
                    rest_of_content = lines[1] if len(lines) > 1 else ""
                    f.write(shebang + '\n' + formatted_header + rest_of_content)
                else:
                    f.write(formatted_header + content)
        except Exception as e:
            print(f"Could not write to file {file_path}: {e}")


def _match_path_parts(path_parts, pattern_parts):
    """Recursively matches path components against pattern components."""
    if not pattern_parts:
        return not path_parts
    if not path_parts:
        return pattern_parts == ['**'] or all(p == '' for p in pattern_parts)

    p_part = pattern_parts[0]
    if p_part == '**':
        if len(pattern_parts) == 1:
            return True  # `/**` at the end matches everything remaining
        # `/**/` can match zero or more directories.
        for i in range(len(path_parts) + 1):
            if _match_path_parts(path_parts[i:], pattern_parts[1:]):
                return True
        return False
    else:
        if fnmatch.fnmatch(path_parts[0], p_part):
            return _match_path_parts(path_parts[1:], pattern_parts[1:])
        return False

def is_path_excluded(relative_path, exclude_patterns):
    """Checks if a relative path matches any of the .gitignore-style exclude patterns."""
    relative_path = relative_path.replace(os.path.sep, '/')
    path_parts = relative_path.split('/')

    for pattern in exclude_patterns:
        pattern = pattern.replace(os.path.sep, '/')
        if '/' not in pattern:
            # If no slash, match against any component of the path
            if any(fnmatch.fnmatch(part, pattern) for part in path_parts):
                return True
        else:
            # If slash is present, match from the root
            pattern_parts = pattern.split('/')
            if _match_path_parts(path_parts, pattern_parts):
                return True
    return False


def apply_headers_to_tree(root_dir, excludes=None, dry_run=False):
    """
    Applies headers to all files in a repository, respecting excludes.
    """
    year = datetime.datetime.now().year
    header_text = APACHE_HEADER.format(year=year)

    all_excludes = DEFAULT_EXCLUDES + (excludes or [])
    print(f"Excluding patterns: {all_excludes}")

    for root, dirs, files in os.walk(root_dir, topdown=True):
        rel_root = os.path.relpath(root, root_dir)
        if rel_root == '.':
            rel_root = ''

        # Filter dirs in-place so os.walk doesn't recurse into them
        dirs[:] = [d for d in dirs if not is_path_excluded(os.path.join(rel_root, d), all_excludes)]

        for file in files:
            rel_path = os.path.join(rel_root, file)
            if is_path_excluded(rel_path, all_excludes):
                continue

            full_path = os.path.join(root, file)
            apply_license_header(full_path, header_text, dry_run)
