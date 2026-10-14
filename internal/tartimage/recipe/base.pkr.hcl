# Virfield developer-v1 recipe for Cirrus Tart images. Derived from
# cirruslabs/macos-image-templates (MIT). Inputs are fixed by the image job.
packer {
  required_plugins {
    tart = {
      version = "= 1.21.0"
      source  = "github.com/cirruslabs/tart"
    }
  }
}

variable "base" { type = string }
variable "vm_name" { type = string }
variable "macos_version" { type = string }
variable "macos_build" { type = string }
variable "sip" { type = string }
variable "security" {
  type    = string
  default = ""
}
source "tart-cli" "base" {
  vm_base_name       = var.base
  vm_name            = var.vm_name
  cpu_count          = 4
  memory_gb          = 8
  disk_size_gb       = 120
  recovery_partition = "relocate"
  display = "1920x1080px"
  headless           = true
  ssh_username       = "admin"
  ssh_password       = "admin"
  ssh_timeout        = "180s"
}

build {
  sources = ["source.tart-cli.base"]

  provisioner "shell" {
    script           = "scripts/check-guest.sh"
    environment_vars = ["VF_MACOS=${var.macos_version}", "VF_BUILD=${var.macos_build}", "VF_SIP=${var.sip}"]
  }
  provisioner "shell" {
    script = "scripts/grow-disk.sh"
  }
  provisioner "shell" {
    script          = "scripts/security.sh"
    execute_command = "if [ '${var.security}' = 'automation' ]; then sudo -n /bin/bash '{{.Path}}' apply admin; fi"
  }
  provisioner "shell" {
    script = "scripts/reset-host-keys.sh"
  }
}
