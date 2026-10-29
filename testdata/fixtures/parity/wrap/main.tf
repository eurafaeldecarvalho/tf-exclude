module "inner" {
  source = "../inner"
}

output "id" {
  value = module.inner.id
}
