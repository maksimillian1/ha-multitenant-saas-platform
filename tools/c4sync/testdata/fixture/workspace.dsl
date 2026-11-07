workspace {
    !identifiers hierarchical
    model {
        sys = softwareSystem "Fixture" {
            db = container "Database"
            broker = container "Broker"
            svc = container "Service" "" "Go" {
                !include golden/components.dsl
            }
        }
    }
    views {
        component sys.svc "Components" {
            include *
            autoLayout lr
        }
    }
}
