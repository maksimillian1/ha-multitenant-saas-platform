workspace {
    !identifiers hierarchical
    model {
        sys = softwareSystem "Fixture" {
            db = container "Database"
            broker = container "Broker"
            svc = container "Service" "" "Go" {
                !include golden/components.dsl
            }
            server = container "Server" "" "Go" {
                !include golden/server.dsl
            }
            migrate = container "Migrate" "" "Go" {
                !include golden/migrate.dsl
            }
        }
    }
    views {
        component sys.server "Server" {
            include *
            autoLayout lr
        }
        component sys.svc "Components" {
            include *
            autoLayout lr
        }
    }
}
