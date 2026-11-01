pluginManagement {
    repositories {
        // This host cannot reach dl.google.com / repo.maven.apache.org directly; keep the mirrors
        // first (same reason as the MCA build), then the canonical repositories for CI.
        maven { url = uri("https://maven.aliyun.com/repository/google") }
        maven { url = uri("https://maven.aliyun.com/repository/gradle-plugin") }
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        // Pinned MCA artifact vendored with checksums; relative so CI needs no machine path.
        maven { url = file("vendor/mca-maven").toURI() }
        maven { url = uri("https://maven.aliyun.com/repository/google") }
        maven { url = uri("https://maven.aliyun.com/repository/public") }
        google()
        mavenCentral()
    }
}

rootProject.name = "AetherCockpitMobile"
include(":app")
