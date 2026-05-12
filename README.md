# genvk

Generate Vulkan 1.4 golang bindings supporting Windows and Mac

`genvk` generates the `vk` package where `vk` is a Go-langauge (and Go-style)
binding around the Vulkan graphics API. Bindings are generated using the
official Vulkan `vk.xml` specification.

Currently only the base vulkan API from the official `vk.xml` 1.4 specification
and only the windows and macos/ios platform bindings are generated. Other API extensions
are currently excluded.

`genvk` is an alternate implementation of, and was originally based on,
[https://github.com/bbredesen/vk-gen](https://github.com/bbredesen/vk-gen).
It is considered ***BETA***. It has been tested on Windows using the golang
[https://github.com/gazed/vu](https://github.com/gazed/vu) game engine.
The mac code is generated, but not yet tested.

## Usage

Download the Vulkan `vk.xml` specification to top level project directory.
Build and run `genvk` to produce the `vk` bindings package.

Ensure that your GPU supports Vulkan and that a Vulkan library is installed in your
system-default library location.  The official Vulkan SDK must also be installed to
run validation layer debugging.
