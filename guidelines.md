# Kubernetes Example Guidelines

Welcome to the `kubernetes/examples` repository! This repository serves as a
community-driven collection of high-quality, educational examples demonstrating
how to run a diverse range of applications, frameworks, and workloads on Kubernetes.
Our goal is to provide a central place for users to discover practical examples,
learn common patterns, and understand best practices for deploying applications,
including general-purpose workloads and specialized AI/ML workloads and platforms.

These guidelines are intended for contributors to ensure that all examples are
consistent, maintainable, easy to understand, and valuable to the Kubernetes community.
By adhering to these guidelines, we can build a rich and up-to-date resource.

## An Example Is...

An example demonstrates running an application, framework, or complex workload
(such as AI/ML model serving, training pipelines, associated toolchains) on Kubernetes.
It must be:

* Meaningful: Solves or illustrates a recognizable, real-world (though potentially simplified) use case.
* Educational: Helps users learn how to deploy and manage the specific type of application
  or pattern on Kubernetes. It should explain the "why" behind the "how."
* Illustrative of Best Practices: Showcases current Kubernetes best practices for configuration,
  security, and application architecture where applicable.
* Focused: While it can involve multiple components, it should illustrate a specific concept
  or setup without becoming overly broad. For very complex systems, consider breaking them into
  smaller, related examples or leveraging modular design, especially for platform blueprints.

## Examples Are NOT...

Examples are intended for educational purposes and should not be:

* Minimalistic Feature Snippets: Examples should be more comprehensive than simple snippets
  designed to illustrate a single Kubernetes API field. Such content is better suited for
  the official [Kubernetes documentation](https://kubernetes.io/docs/home/).
* Pure Kubernetes Feature Demonstrations: Examples should focus on the application running
  on Kubernetes, not solely on demonstrating a Kubernetes feature in isolation (e.g., showing
  high availability is a feature demo, not an application example unless it's core to
  understanding how that specific application achieves HA on Kubernetes).

## An Example Includes...

Each example must be well-structured and documented to ensure clarity and usability.

### Structure and README

* Directory: Each example MUST reside in its own clearly named subdirectory within a
  relevant category (e.g., `/ai/`, `/databases/`).
* README.md: Every example MUST have a `README.md` file. This file is crucial for
  understandability and usability. It SHOULD follow a consistent structure. 
  _(TBD: establish a `EXAMPLE_README_TEMPLATE.md` to guide contributors)._ Key sections include:
    * Title: Clear and descriptive title of the example.
    * Purpose / What You'll Learn: Briefly describe what the example demonstrates and what
      the user will learn by using it.
    * Table of Contents (ToC): For longer READMEs, a ToC is highly recommended.
    * Prerequisites, such as:
        * Required Kubernetes version.
        * Any necessary tools (e.g., `kubectl`, `kustomize`, `helm`, `git`).
        * Specific hardware requirements. This is especially important for AI/ML examples.
    * Quick Start / TL;DR: A concise set of commands to deploy the example with minimal effort,
      preferably without needing to clone the repository.
    * Detailed Steps & Explanation:
        * Walk through the deployment process.
        * Explain the key Kubernetes manifests and their roles within the example.
        * Describe important configuration choices.
    * Verification / Seeing it Work: Commands to verify the application is running correctly,
      along with expected output (console logs, screenshots, or how to access an endpoint).
      For AI/ML examples, this might include how to check training progress or send a test inference request.
    * Configuration Customization (Optional but Recommended): Guidance on how users can
      customize common parameters.
    * Cleanup: Commands to remove all resources created by the example.
    * Troubleshooting (Optional): Common issues and how to resolve them.
    * Further Reading / Next Steps: Links to relevant official documentation, related
      examples, or more advanced topics.
    * Maintainer(s) (Optional): GitHub username(s) of primary maintainers.
    * Last Validated Kubernetes Version (Recommended): The Kubernetes version against
      which the example was last successfully tested.

### Manifests and Configuration

* Clarity and Best Practices: Manifests should be well-commented where non-obvious.
  They must follow [Kubernetes configuration best practices](https://kubernetes.io/docs/concepts/configuration/overview/) 
  and general [security best practices](https://kubernetes.io/docs/concepts/security/overview/).
* Kubernetes API Usage:
    * Use stable APIs whenever possible. If beta or alpha features are used, they must
      be explicitly mentioned along with any necessary feature gates.
    * Avoid deprecated APIs or features. Reference API documentation for core or custom workload APIs.
* Vendor neutral: Do not endorse one particular ecosystem tool or cloud-provider.
    * Individual manifests will inherently use specific tools to fulfill their purpose.
      However, examples will be open to manifests for a diverse set of tools, governed
      by community interest and contribution.
    * If an example *must* use cloud-provider-specific features (e.g., specific AI accelerators,
      managed database services critical to an AI workload/platform), this dependency MUST be
      clearly documented in the `README.md` under prerequisites. If possible, provide guidance
      for adapting to other environments or a generic setup.
* Resource Requests and Limits: Define realistic resource requests and limits for all
  workloads. For resource-intensive examples, clearly document these and, if feasible,
  offer scaled-down versions for resource-constrained environments.
* External Links in Manifests: All URLs used in manifests must point to reliable, versioned sources.
* Container Images:
  * Publicly Accessible Images: Images used in examples MUST be publicly accessible
    from well-known, reputable container registries.
  * Image Tagging: Images MUST be tagged with a specific version instead of using `:latest` tags.
  * If an example requires a custom-built image not available on public registries, the
    `Dockerfile` and all necessary source files (build context) MUST be included within
    the example's subdirectory (e.g., in an `image/` subfolder).

### Documentation and Commands

* Linking to Official Docs: On the first mention of a Kubernetes concept, link to its
  official documentation page.
* Code Highlighting: Use appropriate code highlighting for shell commands and YAML
  manifests, consistent with GitHub's rendering (typically Rouge-supported types).
    * Commands to be copied by the user should use the `shell` syntax highlighting type,
      and do not include any kind of prompt characters (e.g., `$`, `#`).
    * Example output should be in a separate block to distinguish it from the commands.
* Placeholders: When providing commands or configuration where users need to substitute
  their own values, use angle brackets: e.g., `<YOUR_NAMESPACE>`, `<YOUR_BUCKET_NAME>`.
* Screenshots/Diagrams (Optional but helpful): For complex examples, screenshots of the
  running application or simple architecture diagrams can be very beneficial. Store these
  within the example's subdirectory. Asciinema recordings for terminal interactions are
  also welcome.

## Maintenance and Lifecycle

* Review and Updates: Examples are expected to be kept reasonably up-to-date with current
  Kubernetes versions and best practices. Contributors are encouraged to revisit their
  examples periodically. The `README.md` should specify the Kubernetes version(s) against
  which the example was last tested.
* Archiving Policy:
    * A process will be established by SIG Apps for identifying outdated, unmaintained, or
      broken examples.
    * Such examples may be moved to an `archive/` directory or removed after a deprecation
      period and attempts to find new maintainers. This ensures the repository remains a
      reliable source of current best practices.
    * The `README.md` of archived examples should clearly state their archived status and
      potential incompatibility with current Kubernetes versions.
* Community Maintenance: SIG Apps will act as the overall steward, and individual example
  maintainers (original authors or new volunteers) are crucial for the health of the repository.
