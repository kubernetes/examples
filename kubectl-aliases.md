### Useful kubectl aliases

alias k='kubectl'
source <(kubectl completion bash) 
source <(kubectl completion bash|sed "s/kubectl/k/g")

alias kd='k describe '
alias kdp='k describe pod '
alias kdd='k describe deploy '
alias kds='k describe svc '

alias kg='k get '
alias kgp='k get pod '
alias kgd='k get deploy '
alias kgs='k get svc '
alias kgn='k get no '

alias ke='k edit '
alias ka='k apply -f '
alias kc='k create '

alias kdl='k delete --force '
alias kdlf='k delete --force -f '

alias kl='k logs '

alias kcv='k config view '
alias kgc='k config get-contexts '
alias kcc='k config current-context '
alias ksn='k config set-context  --current  --namespace ' 
alias kuc='k config use-context '
alias ksc='k config set-context '

alias kwget='k run busy-${RANDOM} --image=busybox:1.28.4 --restart=Never --rm -it -- wget -O- -T 2 -t 2  '
alias kns='k run busy-${RANDOM} --image=busybox:1.28.4 --restart=Never --rm -it -- nslookup  '

export do="--dry-run=client -oyaml"
export VISUAL='vi'   #default editor program
export EDITOR='vi'   #default editor program
export PS1="[\u@\h \T  \W]\\$ "   #to set time in the command prompt
