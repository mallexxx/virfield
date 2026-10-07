# Cirrus disable-sip template (cirruslabs/macos-image-templates, MIT) with the
# plugin pinned. Recovery is driven over VNC; the VM is changed in place.
packer {
  required_plugins {
    tart = {
      version = "= 1.21.0"
      source  = "github.com/cirruslabs/tart"
    }
  }
}

variable "vm_name" { type = string }

source "tart-cli" "tart" {
  vm_name      = var.vm_name
  recovery     = true
  cpu_count    = 4
  memory_gb    = 8
  communicator = "none"
  boot_command = [
    # Skip over "Macintosh" and select "Options" to boot into macOS Recovery
    "<wait60s><right><right><enter>",
    # Open Terminal
    "<wait10s><leftAltOn>T<leftAltOff>",
    # Disable SIP
    "<wait10s>csrutil disable<enter>",
    "<wait10s>y<enter>",
    "<wait10s>admin<enter>",
    # Shutdown
    "<wait10s>halt<enter>"
  ]
}

build {
  sources = ["source.tart-cli.tart"]
}
