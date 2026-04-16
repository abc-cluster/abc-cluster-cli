set shell := ["bash", "-euo", "pipefail", "-c"]

# Build abc and install it into ~/bin.
install-local:
    mkdir -p "${HOME}/bin"
    go build -o abc .
    mv abc "${HOME}/bin/abc"
    chmod 0755 "${HOME}/bin/abc"
    echo "Installed ${HOME}/bin/abc"
