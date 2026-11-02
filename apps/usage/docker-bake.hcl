# Builds the three usage images from one Dockerfile:
#   docker buildx bake -f apps/usage/docker-bake.hcl --load
# TAG defaults to dev, the tag the kind prototype runs (prototype/create-cluster.sh).
variable "TAG" {
  default = "dev"
}

group "default" {
  targets = ["writeapi", "writer", "migrate"]
}

target "_common" {
  context    = "."
  dockerfile = "Dockerfile"
}

target "writeapi" {
  inherits = ["_common"]
  target   = "writeapi"
  tags     = ["hamsp/usage-writeapi:${TAG}"]
}

target "writer" {
  inherits = ["_common"]
  target   = "writer"
  tags     = ["hamsp/usage-writer:${TAG}"]
}

target "migrate" {
  inherits = ["_common"]
  target   = "migrate"
  tags     = ["hamsp/usage-migrate:${TAG}"]
}
