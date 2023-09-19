import datetime
import os
import subprocess
import yaml
from git import Repo
import filecmp

# Custom configuration for the Sphinx documentation builder.
# All configuration specific to your project should be done in this file.
#
# The file is included in the common conf.py configuration file.
# You can modify any of the settings below or add any configuration that
# is not covered by the common conf.py file.
#
# For the full list of built-in configuration values, see the documentation:
# https://www.sphinx-doc.org/en/master/usage/configuration.html

############################################################
### Project information
############################################################

# Product name
project = 'Canonical LXD'
author = 'LXD contributors'

# Uncomment if your product uses release numbers
# release = '1.0'

with open("../shared/version/flex.go") as fd:
    version = fd.read().split("\n")[-2].split()[-1].strip("\"")

# The default value uses the current year as the copyright year.
#
# For static works, it is common to provide the year of first publication.
# Another option is to give the first year and the current year
# for documentation that is often changed, e.g. 2022–2023 (note the en-dash).
#
# A way to check a GitHub repo's creation date is to obtain a classic GitHub
# token with 'repo' permissions here: https://github.com/settings/tokens
# Next, use 'curl' and 'jq' to extract the date from the GitHub API's output:
#
# curl -H 'Authorization: token <TOKEN>' \
#   -H 'Accept: application/vnd.github.v3.raw' \
#   https://api.github.com/repos/canonical/<REPO> | jq '.created_at'

copyright = '2014-%s %s' % (datetime.date.today().year, author)

## Open Graph configuration - defines what is displayed in the website preview
# The URL of the documentation output
ogp_site_url = 'https://documentation.ubuntu.com/lxd/en/latest/'
# The documentation website name (usually the same as the product name)
ogp_site_name = 'LXD documentation'
# An image or logo that is used in the preview
ogp_image = 'https://documentation.ubuntu.com/lxd/en/latest/_static/tag.png'

# Update with the favicon for your product (default is the circle of friends)
html_favicon = '.sphinx/_static/favicon.ico'

# (Some settings must be part of the html_context dictionary, while others
#  are on root level. Don't move the settings.)
html_context = {

    # Change to the link to your product website (without "https://")
    'product_page': 'ubuntu.com/lxd',

    # Add your product tag to ".sphinx/_static" and change the path
    # here (start with "_static"), default is the circle of friends
    'product_tag': '_static/tag.png',

    # Change to the discourse instance you want to be able to link to
    # using the :discourse: metadata at the top of a file
    # (use an empty value if you don't want to link)
    'discourse': 'https://discourse.ubuntu.com/c/lxd/',

    # ru-fu: we're using different Discourses
    'discourse_prefix': {
        'lxc': 'https://discuss.linuxcontainers.org/t/',
        'ubuntu': 'https://discourse.ubuntu.com/t/'
    },

    # Change to the GitHub info for your project
    'github_url': 'https://github.com/canonical/lxd',

    # Change to the branch for this version of the documentation
    'github_version': 'main',

    # Change to the folder that contains the documentation
    # (usually "/" or "/docs/")
    'github_folder': '/doc/',

    # Change to an empty value if your GitHub repo doesn't have issues enabled.
    # This will disable the feedback button and the issue link in the footer.
    'github_issues': 'enabled'
}

# If your project is on documentation.ubuntu.com, specify the project
# slug (for example, "lxd") here.
slug = "lxd"

############################################################
### Redirects
############################################################

# Set up redirects (https://documatt.gitlab.io/sphinx-reredirects/usage.html)
# For example: 'explanation/old-name.html': '../how-to/prettify.html',

redirects = {
    'howto/instances_snapshots/index': '../instances_backup/',
    'reference/network_external/index': '../networks/',
}

############################################################
### Link checker exceptions
############################################################

# Links to ignore when checking links

linkcheck_ignore = [
    'https://127.0.0.1:8443/1.0',
    'https://web.libera.chat/#lxd',
    'http://localhost:8001',
    r'/lxd/en/latest/api/.*',
    r'/api/.*'
    ]

linkcheck_exclude_documents = [r'.*/manpages/.*']

############################################################
### Additions to default configuration
############################################################

## The following settings are appended to the default configuration.
## Use them to extend the default functionality.

# Add extensions
custom_extensions = [
    'sphinx.ext.intersphinx',
    'config-options',
    'sphinx_remove_toctrees'
]

# Add files or directories that should be excluded from processing.
custom_excludes = [
    'html',
    'README.md',
    'config_options_cheat_sheet.md'
]

# Add CSS files (located in .sphinx/_static/)
custom_html_css_files = []

# Add JavaScript files (located in .sphinx/_static/)
custom_html_js_files = []

## The following settings override the default configuration.

# Specify a reST string that is included at the end of each file.
# If commented out, use the default (which pulls the reuse/links.txt
# file into each reST file).
# custom_rst_epilog = ''

# By default, the documentation includes a feedback button at the top.
# You can disable it by setting the following configuration to True.
disable_feedback_button = False

custom_tags = []

############################################################
### Additional configuration
############################################################

## Add any configuration that is not covered by the common conf.py file.

myst_linkify_fuzzy_links=False
myst_heading_anchors = 7

if os.path.exists('./substitutions.yaml'):
    with open('./substitutions.yaml', 'r') as fd:
        myst_substitutions = yaml.safe_load(fd.read())
if os.path.exists('./related_topics.yaml'):
    with open('./related_topics.yaml', 'r') as fd:
        myst_substitutions.update(yaml.safe_load(fd.read()))

if ('LOCAL_SPHINX_BUILD' in os.environ) and (os.environ['LOCAL_SPHINX_BUILD'] == 'True'):
    swagger_url_scheme = '/api/#{{path}}'
else:
    swagger_url_scheme = '/lxd/en/latest/api/#{{path}}'

myst_url_schemes = {
    'http': None,
    'https': None,
    'swagger': swagger_url_scheme,
}

remove_from_toctrees = ['reference/manpages/lxc/*.md']

intersphinx_mapping = {
    'cloud-init': ('https://cloudinit.readthedocs.io/en/latest/', None)
}

html_extra_path = ['.sphinx/_extra']

html_theme_options = {
    "sidebar_hide_name": True,
}

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

if ('LOCAL_SPHINX_BUILD' in os.environ and
    os.environ['LOCAL_SPHINX_BUILD'] == 'True'):
    path = str(subprocess.check_output(['go', 'env', 'GOPATH'], encoding='utf-8').strip())
    lxc = os.path.join(path, 'bin', 'lxc')
    if os.path.isfile(lxc):
        print('Using ' + lxc + ' to generate man pages.')
    else:
        print('Cannot find lxc in ' + lxc)
        exit(2)
else:
    lxc = '../lxc.bin'

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

if ('TOPICAL' in os.environ) and (os.environ['TOPICAL'] == 'True'):
    root_doc = 'index_topical'
    custom_excludes.extend(['index.md','tutorial/index.md','howto/index.md','explanation/index.md','reference/index.md','howto/troubleshoot.md'])
    redirects['index/index'] = '../index_topical/'
    redirects['index'] = '../index_topical/'
    custom_tags.append('topical')
else:
    custom_excludes.extend(['index_topical.md','security.md','external_resources.md','reference/network_external.md'])
    redirects['security/index'] = '../explanation/security/'
    custom_tags.append('diataxis')
