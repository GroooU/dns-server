variable "yc_token" {
  description = "Yandex Cloud IAM token or OAuth token"
  type        = string
  sensitive   = true
}

variable "yc_cloud_id" {
  description = "Yandex Cloud cloud ID"
  type        = string
}

variable "yc_folder_id" {
  description = "Yandex Cloud folder ID"
  type        = string
}

variable "yc_zone" {
  description = "Availability zone"
  type        = string
  default     = "ru-central1-a"
}

variable "ssh_public_key" {
  description = "SSH public key content (e.g. contents of ~/.ssh/id_rsa.pub)"
  type        = string
}
