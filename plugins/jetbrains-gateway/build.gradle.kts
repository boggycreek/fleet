// Copyright (c) 2026 Boggy Creek Software LLC
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

plugins {
    id("java")
    id("org.jetbrains.kotlin.jvm") version "1.9.24"
    id("org.jetbrains.intellij.platform") version "2.0.0"
}

group = "com.boggycreek.sndbx"
version = "0.1.0-alpha"

repositories {
    mavenCentral()
    intellijPlatform {
        defaultRepositories()
    }
}

dependencies {
    intellijPlatform {
        gateway("2024.2")
        bundledPlugin("com.intellij.modules.platform")
        bundledPlugin("com.jetbrains.gateway")
        instrumentationTools()
    }
}

intellijPlatform {
    pluginConfiguration {
        id = "com.boggycreek.fleet.gateway"
        name = "Fleet Gateway"
        version = project.version.toString()
        vendor {
            name = "Boggy Creek Software LLC"
            email = "support@boggycreek.com"
            url = "https://github.com/boggycreek/fleet"
        }
    }
}
