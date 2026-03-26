pipeline {
    agent {
        node {
            label 'rhel8-spot'
        }
    }

    options {
        timestamps()
    }

    stages {
        stage('PR Check') {
            when {
                changeRequest()
            }
            steps {
                sh './pr_check.sh'
            }
        }

        stage('Build') {
            when {
                branch 'main'
            }
            steps {
                wrap([$class: 'VaultBuildWrapper',
                    vaultSecrets: [
                        [
                            path: 'app-sre/quay/app-sre-push',
                            secretValues: [
                                [envVar: 'QUAY_USER', vaultKey: 'user'],
                                [envVar: 'QUAY_TOKEN', vaultKey: 'token']
                            ]
                        ]
                    ]
                ]) {
                    sh './build_deploy.sh'
                }
            }
        }
    }

    post {
        always {
            cleanWs()
        }
    }
}
