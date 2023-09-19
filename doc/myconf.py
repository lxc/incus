import contextlib
import datetime
import os
import stat
import subprocess
import tempfile
import yaml
from git import Repo
import wget
import filecmp

# Download and link swagger-ui files
if not os.path.isdir('.sphinx/deps/swagger-ui'):
    Repo.clone_from('https://github.com/swagger-api/swagger-ui', '.sphinx/deps/swagger-ui', depth=1)

os.makedirs('.sphinx/_static/swagger-ui/', exist_ok=True)

if not os.path.islink('.sphinx/_static/swagger-ui/swagger-ui-bundle.js'):
    os.symlink('../../deps/swagger-ui/dist/swagger-ui-bundle.js', '.sphinx/_static/swagger-ui/swagger-ui-bundle.js')
if not os.path.islink('.sphinx/_static/swagger-ui/swagger-ui-standalone-preset.js'):
    os.symlink('../../deps/swagger-ui/dist/swagger-ui-standalone-preset.js', '.sphinx/_static/swagger-ui/swagger-ui-standalone-preset.js')
if not os.path.islink('.sphinx/_static/swagger-ui/swagger-ui.css'):
    os.symlink('../../deps/swagger-ui/dist/swagger-ui.css', '.sphinx/_static/swagger-ui/swagger-ui.css')

### MAN PAGES ###

# Find path to lxc client (different for local builds and on RTD)

if ("LOCAL_SPHINX_BUILD" in os.environ and
    os.environ["LOCAL_SPHINX_BUILD"] == "True"):
    path = str(subprocess.check_output(['go', 'env', 'GOPATH'], encoding="utf-8").strip())
    lxc = os.path.join(path, 'bin', 'lxc')
    if os.path.isfile(lxc):
        print("Using " + lxc + " to generate man pages.")
    else:
        print("Cannot find lxc in " + lxc)
        exit(2)
else:
    lxc = "../lxc.bin"

# Generate man pages content

os.makedirs('.sphinx/deps/manpages', exist_ok=True)
subprocess.run([lxc, 'manpage', '.sphinx/deps/manpages/', '--format=md'],
               check=True)

# Preprocess man pages content

for page in [x for x in os.listdir('.sphinx/deps/manpages')
             if os.path.isfile(os.path.join('.sphinx/deps/manpages/', x))]:

    # replace underscores with slashes to create a directory structure
    pagepath = page.replace('_', '/')

    # for each generated page, add an anchor, fix the title, and adjust the
    # heading levels
    with open(os.path.join('.sphinx/deps/manpages/', page), 'r') as mdfile:
        content = mdfile.readlines()

    os.makedirs(os.path.dirname(os.path.join('.sphinx/deps/manpages/', pagepath)),
                exist_ok=True)

    with open(os.path.join('.sphinx/deps/manpages/', pagepath), 'w') as mdfile:
        mdfile.write('(' + page + ')=\n')
        for line in content:
            if line.startswith('###### Auto generated'):
                continue
            elif line.startswith('## '):
                mdfile.write('# `' + line[3:].rstrip() + '`\n')
            elif line.startswith('##'):
                mdfile.write(line[1:])
            else:
                mdfile.write(line)

    # remove the input page (unless the file path doesn't change)
    if '_' in page:
        os.remove(os.path.join('.sphinx/deps/manpages/', page))

# Complete and copy man pages content

for folder, subfolders, files in os.walk('.sphinx/deps/manpages'):

    # for each subfolder, add toctrees to the parent page that
    # include the subpages
    for subfolder in subfolders:
        with open(os.path.join(folder, subfolder + '.md'), 'a') as parent:
            parent.write('```{toctree}\n:titlesonly:\n:glob:\n:hidden:\n\n' +
                         subfolder + '/*\n```\n')

    # for each file, if the content is different to what has been generated
    # before, copy the file to the reference/manpages folder
    # (copying all would mess up the incremental build)
    for f in files:
        sourcefile = os.path.join(folder, f)
        targetfile = os.path.join('reference/manpages/',
                                  os.path.relpath(folder,
                                                  '.sphinx/deps/manpages'),
                                  f)

        if (not os.path.isfile(targetfile) or
            not filecmp.cmp(sourcefile, targetfile, shallow=False)):

            os.makedirs(os.path.dirname(targetfile), exist_ok=True)
            os.system('cp ' + sourcefile + ' ' + targetfile)

### End MAN PAGES ###

# Project config.
project = "LXD"
author = "LXD contributors"
copyright = "2014-%s %s" % (datetime.date.today().year, author)

with open("../shared/version/flex.go") as fd:
    version = fd.read().split("\n")[-2].split()[-1].strip("\"")

# Extensions.
extensions = [
    "myst_parser",
    "sphinx_design",
    "sphinx_tabs.tabs",
    "sphinx_reredirects",
    "sphinxext.opengraph",
    "youtube-links",
    "related-links",
    "custom-rst-roles",
    "sphinxcontrib.jquery",
    "sphinx_copybutton",
    "sphinx.ext.intersphinx",
    "terminal-output",
    "config-options",
    "notfound.extension",
    "sphinx_remove_toctrees"
]

myst_enable_extensions = [
    "substitution",
    "deflist",
    "linkify"
]

myst_linkify_fuzzy_links=False
myst_heading_anchors = 7

if os.path.exists("./substitutions.yaml"):
    with open("./substitutions.yaml", "r") as fd:
        myst_substitutions = yaml.safe_load(fd.read())
if os.path.exists("./related_topics.yaml"):
    with open("./related_topics.yaml", "r") as fd:
        myst_substitutions.update(yaml.safe_load(fd.read()))

intersphinx_mapping = {
    'cloud-init': ('https://cloudinit.readthedocs.io/en/latest/', None)
}

notfound_urls_prefix = "/lxd/en/latest/"

if ("LOCAL_SPHINX_BUILD" in os.environ) and (os.environ["LOCAL_SPHINX_BUILD"] == "True"):
    swagger_url_scheme = "/api/#{{path}}"
else:
    swagger_url_scheme = "/lxd/en/latest/api/#{{path}}"

myst_url_schemes = {
    "http": None,
    "https": None,
    "swagger": swagger_url_scheme,
}

remove_from_toctrees = ["reference/manpages/lxc/*.md"]

# Setup theme.
templates_path = [".sphinx/_templates"]

html_theme = "furo"
html_show_sphinx = False
html_last_updated_fmt = ""
html_favicon = ".sphinx/_static/favicon.ico"
html_static_path = ['.sphinx/_static']
html_css_files = ['custom.css', 'header.css', 'furo_colors.css']
html_js_files = ['header-nav.js']
html_extra_path = ['.sphinx/_extra']

html_theme_options = {
    "sidebar_hide_name": True,
}

html_context = {
    "github_url": "https://github.com/canonical/lxd",
    "github_version": "main",
    "github_folder": "/doc/",
    "github_filetype": "md",
    "discourse_prefix": {
        "lxc": "https://discuss.linuxcontainers.org/t/",
        "ubuntu": "https://discourse.ubuntu.com/t/"}
}

source_suffix = ".md"

# List of patterns, relative to source directory, that match files and
# directories to ignore when looking for source files.
# This pattern also affects html_static_path and html_extra_path.
exclude_patterns = ['html', 'README.md', '.sphinx', 'config_options_cheat_sheet.md']

# Open Graph configuration

ogp_site_url = "https://documentation.ubuntu.com/lxd/en/latest/"
ogp_site_name = "LXD documentation"
ogp_image = "https://documentation.ubuntu.com/lxd/en/latest/_static/tag.png"

# Links to ignore when checking links

linkcheck_ignore = [
    'https://127.0.0.1:8443/1.0',
    'https://web.libera.chat/#lxd',
    'http://localhost:8001',
    r'/lxd/en/latest/api/.*',
    r'/api/.*'
]
linkcheck_exclude_documents = [r'.*/manpages/.*']

linkcheck_anchors_ignore_for_url = [
    r'https://github\.com/.*'
]

# Setup redirects (https://documatt.gitlab.io/sphinx-reredirects/usage.html)
redirects = {
    "howto/instances_snapshots/index": "../instances_backup/",
    "reference/network_external/index": "../networks/",
}


if ("TOPICAL" in os.environ) and (os.environ["TOPICAL"] == "True"):
    root_doc = "index_topical"
    exclude_patterns.extend(['index.md','tutorial/index.md','howto/index.md','explanation/index.md','reference/index.md','howto/troubleshoot.md'])
    redirects["index/index"] = "../index_topical/"
    redirects["index"] = "../index_topical/"
    tags.add('topical')
else:
    exclude_patterns.extend(['index_topical.md','security.md','external_resources.md','reference/network_external.md'])
    redirects["security/index"] = "../explanation/security/"
    tags.add('diataxis')
