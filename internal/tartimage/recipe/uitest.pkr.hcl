# Virfield uitest-27-v1 recipe for a Cirrus macOS 27 Xcode image (SIP ships
# disabled; check-guest enforces it). The disk is not resized: the plugin
# cannot relocate this image's recovery partition, and grow-disk gates on the
# free space the upstream already has. Inputs are fixed by the image job.
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

source "tart-cli" "uitest" {
  vm_base_name = var.base
  vm_name      = var.vm_name
  cpu_count    = 4
  memory_gb    = 8
  display = "1920x1080"
  recovery_partition = "keep"
  headless     = true
  ssh_username = "admin"
  ssh_password = "admin"
  ssh_timeout  = "180s"
}

build {
  sources = ["source.tart-cli.uitest"]

  provisioner "shell" {
    script           = "scripts/check-guest.sh"
    environment_vars = ["VF_MACOS=${var.macos_version}", "VF_BUILD=${var.macos_build}", "VF_SIP=disabled"]
  }
  provisioner "shell" {
    script = "scripts/grow-disk.sh"
  }
  provisioner "shell" {
    script = "scripts/adopt-xcode.sh"
  }
  provisioner "shell" {
    script           = "scripts/provision27.sh"
    timeout          = "1h"
    environment_vars = ["VF_PASSWORD=admin"]
  }
  provisioner "shell" {
    script = "scripts/reset-host-keys.sh"
  }
}
