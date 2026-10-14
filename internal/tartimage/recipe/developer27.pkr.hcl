# Virfield macOS 27 recipe for Cirrus Tart images. Derived from
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
variable "xip" { type = string }
variable "xip_sha1" { type = string }
variable "xcode_version" { type = string }
variable "xcode_build" { type = string }

source "tart-cli" "developer" {
  vm_base_name = var.base
  vm_name      = var.vm_name
  cpu_count    = 4
  memory_gb    = 8
  display = "1920x1080px"
  recovery_partition = "keep"
  headless     = true
  ssh_username = "admin"
  ssh_password = "admin"
  ssh_timeout  = "180s"
}

build {
  sources = ["source.tart-cli.developer"]

  provisioner "shell" {
    script           = "scripts/check-guest.sh"
    environment_vars = ["VF_MACOS=${var.macos_version}", "VF_BUILD=${var.macos_build}", "VF_SIP=${var.sip}"]
  }
  provisioner "shell" {
    script = "scripts/grow-disk.sh"
  }
  provisioner "shell" {
    inline = ["umask 077", "mkdir -p \"$HOME/.virfield-xcode\""]
  }
  provisioner "file" {
    source      = var.xip
    destination = "/Users/admin/.virfield-xcode/archive.xip"
  }
  provisioner "shell" {
    script           = "scripts/install-xcode.sh"
    timeout          = "2h"
    environment_vars = ["VF_XIP_SHA1=${var.xip_sha1}", "VF_XCODE_VERSION=${var.xcode_version}", "VF_XCODE_BUILD=${var.xcode_build}"]
  }
  provisioner "shell" {
    script  = "scripts/xcode-post.sh"
    timeout = "1h"
  }
  provisioner "shell" {
    script          = "scripts/security.sh"
    execute_command = "if [ '${var.security}' = 'automation' ]; then sudo -n /bin/bash '{{.Path}}' apply admin; fi"
  }
  provisioner "shell" {
    script = "scripts/reset-host-keys.sh"
  }
}
