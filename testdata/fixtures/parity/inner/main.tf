resource "terraform_data" "deep" {}

output "id" {
  value = terraform_data.deep.output
}
